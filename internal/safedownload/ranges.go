package safedownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const minParallelSize = 32 << 20
const rangeSize = 8 << 20
const rangeBuffer = 128 << 10

var errRangesUnsupported = errors.New("server does not support verified byte ranges")

func downloadWorkers() int {
	n := 8
	if runtime.GOOS == "android" {
		n = 4
	}
	if v, err := strconv.Atoi(os.Getenv("NODEXLM_DOWNLOAD_CONNECTIONS")); err == nil && v >= 1 {
		n = min(v, 16)
	}
	return n
}

func strongETag(s string) bool {
	return len(s) >= 2 && strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") && !strings.HasPrefix(s, "W/")
}

// A small adaptive admission window shares retry cooldown across all workers.
// It grows only after completed work and shrinks on server congestion or
// persistent storage backpressure. No additional probing requests.
type rangeWindow struct {
	mu                            sync.Mutex
	limit, max, active, completed int
	until                         time.Time
	changed                       chan struct{}
}

func newRangeWindow(maximum int) *rangeWindow {
	initial := 4
	if runtime.GOOS == "android" {
		initial = 2
	}
	return &rangeWindow{limit: min(initial, maximum), max: maximum, changed: make(chan struct{})}
}

func (w *rangeWindow) acquire(ctx context.Context) error {
	for {
		w.mu.Lock()
		delay := time.Until(w.until)
		if delay <= 0 && w.active < w.limit {
			w.active++
			w.mu.Unlock()
			return nil
		}
		changed := w.changed
		w.mu.Unlock()
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-changed:
				timer.Stop()
			case <-timer.C:
			}
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-changed:
			}
		}
	}
}

type rangeThrottle struct {
	status int
	delay  time.Duration
}

func (e *rangeThrottle) Error() string { return fmt.Sprintf("download throttled (HTTP %d)", e.status) }

func retryAfter(value string) time.Duration {
	if n, err := strconv.Atoi(value); err == nil && n >= 0 {
		return time.Duration(min(n, 300)) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		return max(time.Second, min(5*time.Minute, time.Until(date)))
	}
	return 2 * time.Second
}

func (w *rangeWindow) release(err error, slowDisk bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.active--
	var throttle *rangeThrottle
	if errors.As(err, &throttle) {
		w.limit = max(1, w.limit/2)
		w.until = maxTime(w.until, time.Now().Add(throttle.delay))
		w.completed = 0
	} else if slowDisk {
		w.limit = max(1, w.limit-1)
		w.completed = 0
	} else if err == nil {
		w.completed++
		if w.completed >= 8 && w.limit < w.max {
			w.limit++
			w.completed = 0
		}
	}
	close(w.changed)
	w.changed = make(chan struct{})
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

type rangeJournal struct {
	Meta partial `json:"meta"`
	Done []bool  `json:"done"`
}

func requestRange(ctx context.Context, client *http.Client, source string, meta partial, start, end int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "NodexLM (GGUF downloader)")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end-1))
	req.Header.Set("If-Range", meta.Validator)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 429 || resp.StatusCode == 503 {
		delay := retryAfter(resp.Header.Get("Retry-After"))
		resp.Body.Close()
		return nil, &rangeThrottle{status: resp.StatusCode, delay: delay}
	}
	want := fmt.Sprintf("bytes %d-%d/%d", start, end-1, meta.Total)
	if resp.StatusCode == 200 || resp.StatusCode == 416 {
		resp.Body.Close()
		return nil, errRangesUnsupported
	}
	if resp.StatusCode != 206 || resp.Header.Get("Content-Range") != want ||
		resp.Header.Get("ETag") != meta.Validator ||
		(resp.ContentLength >= 0 && resp.ContentLength != end-start) ||
		(resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity") {
		resp.Body.Close()
		return nil, fmt.Errorf("unverified range response (HTTP %d)", resp.StatusCode)
	}
	return resp, nil
}

// Checkpoints record only completed ranges after syncing the staging file.
// Keep a separate filename so the old sequential resume never interprets a
// sparse pre-sized file as a contiguous prefix.
func saveRanges(file *os.File, path string, j rangeJournal) error {
	if err := file.Sync(); err != nil {
		return err
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".ranges-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(raw); err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp.Name(), path)
}

func fetchRanges(ctx context.Context, client *http.Client, source, target string, meta partial, progress func(int64, int64)) (int64, error) {
	count := int((meta.Total + rangeSize - 1) / rangeSize)
	if count <= 0 || count > 32768 {
		return 0, errRangesUnsupported
	}
	// A real GET, not Accept-Ranges or HEAD, proves exact Range semantics.
	probe, err := requestRange(ctx, client, source, meta, 0, 1)
	if err != nil {
		return 0, err
	}
	var sample [1]byte
	_, err = io.ReadFull(probe.Body, sample[:])
	probe.Body.Close()
	if err != nil {
		return 0, err
	}
	stage, journal := target+".ranges", target+".ranges.json"
	for _, path := range []string{stage, journal} {
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return 0, errors.New("invalid segmented staging path")
		} else if err != nil && !os.IsNotExist(err) {
			return 0, err
		}
	}
	file, err := os.OpenFile(stage, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	j := rangeJournal{Meta: meta, Done: make([]bool, count)}
	valid := false
	if info, err := os.Stat(journal); err == nil && info.Size() <= 1<<20 {
		var saved rangeJournal
		if raw, err := os.ReadFile(journal); err == nil && json.Unmarshal(raw, &saved) == nil &&
			saved.Meta == meta && len(saved.Done) == count {
			if st, err := file.Stat(); err == nil && st.Size() == meta.Total {
				j, valid = saved, true
			}
		}
	}
	var received int64
	for i, done := range j.Done {
		if done {
			start := int64(i) * rangeSize
			received += min(int64(rangeSize), meta.Total-start)
		}
	}
	free, err := FreeBytes(filepath.Dir(stage))
	if err != nil {
		return received, err
	}
	if uint64(meta.Total-received) > free {
		return received, errors.New("insufficient disk space for segmented model")
	}
	if !valid {
		if err := file.Truncate(0); err != nil {
			return 0, err
		}
		if err := file.Truncate(meta.Total); err != nil {
			return 0, err
		}
		if err := saveRanges(file, journal, j); err != nil {
			return 0, err
		}
	}
	if progress != nil {
		progress(received, meta.Total)
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	var failure error
	next := 0
	checkpoint := received
	lastSync := time.Now()
	fail := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if failure == nil {
			failure = err
			cancel()
		}
	}
	window := newRangeWindow(min(workersFor(ctx), count))
	for range window.max {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, rangeBuffer)
			for workCtx.Err() == nil {
				mu.Lock()
				for next < count && j.Done[next] {
					next++
				}
				i := next
				next++
				mu.Unlock()
				if i >= count {
					return
				}
				start := int64(i) * rangeSize
				end := min(start+rangeSize, meta.Total)
				var err error
				for attempt := 0; attempt < 4; attempt++ {
					if err = window.acquire(workCtx); err != nil {
						break
					}
					var slowDisk bool
					err = writeRange(workCtx, client, source, meta, file, buf, start, end, &slowDisk)
					window.release(err, slowDisk)
					if err == nil || errors.Is(err, errRangesUnsupported) || workCtx.Err() != nil {
						break
					}
					select {
					case <-workCtx.Done():
					case <-time.After(time.Duration(1<<attempt) * time.Second):
					}
				}
				if err != nil {
					fail(err)
					return
				}
				mu.Lock()
				j.Done[i] = true
				received += end - start
				if progress != nil {
					progress(received, meta.Total)
				}
				if received-checkpoint >= 32<<20 || time.Since(lastSync) >= 5*time.Second {
					err = saveRanges(file, journal, j)
					checkpoint, lastSync = received, time.Now()
				}
				mu.Unlock()
				if err != nil {
					fail(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := saveRanges(file, journal, j); err != nil {
		return received, err
	}
	if failure != nil {
		if errors.Is(failure, errRangesUnsupported) {
			file.Close()
			_ = os.Remove(stage)
			_ = os.Remove(journal)
		}
		return received, failure
	}
	if err := ctx.Err(); err != nil {
		return received, err
	}
	if received != meta.Total {
		return received, errors.New("incomplete segmented model")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, &contextReader{ctx: ctx, reader: io.NewSectionReader(file, 0, meta.Total)}); err != nil {
		return received, err
	}
	var magic [4]byte
	_, err = file.ReadAt(magic[:], 0)
	if err != nil || string(magic[:]) != "GGUF" || hex.EncodeToString(digest.Sum(nil)) != meta.SHA256 {
		file.Close()
		_ = os.Remove(stage)
		_ = os.Remove(journal)
		return received, errors.New("segmented model integrity mismatch")
	}
	if err := file.Close(); err != nil {
		return received, err
	}
	if err := os.Rename(stage, target); err != nil {
		return received, err
	}
	_ = os.Remove(journal)
	// Preserve the verified identity until the caller publishes the file.
	if raw, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(target+".json", raw, 0600)
	}
	return received, nil
}

func writeRange(ctx context.Context, client *http.Client, source string, meta partial, file *os.File, buf []byte, start, end int64, slowDisk *bool) error {
	resp, err := requestRange(ctx, client, source, meta, start, end)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	pos := start
	var writeTime time.Duration
	started := time.Now()
	defer func() { *slowDisk = writeTime >= 250*time.Millisecond && writeTime > time.Since(started)/2 }()
	for pos < end {
		n, err := io.ReadFull(resp.Body, buf[:min(int64(len(buf)), end-pos)])
		if n > 0 {
			if err := paceDownload(ctx, n); err != nil {
				return err
			}
			before := time.Now()
			written, writeErr := file.WriteAt(buf[:n], pos)
			writeTime += time.Since(before)
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
			pos += int64(n)
		}
		if err != nil {
			return err
		}
	}
	var extra [1]byte
	if n, err := resp.Body.Read(extra[:]); n != 0 || err != io.EOF {
		return errors.New("range body exceeds declared length")
	}
	return nil
}

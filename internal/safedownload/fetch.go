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
	"strconv"
	"strings"
)

// A transfer owns its staging file until it returns. Serialize transfers within
// this process so CLI and dashboard cannot append to the same partial file.
var transfers = make(chan struct{}, 1)

type partial struct {
	Source    string `json:"source"`
	Validator string `json:"validator"`
	SHA256    string `json:"sha256,omitempty"`
	Total     int64  `json:"total"`
}

// StagePath is stable across restarts and unique per source URL. Staging files
// are hidden from the model scanner and retained after network interruption.
func StagePath(directory, source string) string {
	sum := sha256.Sum256([]byte(source))
	return filepath.Join(directory, fmt.Sprintf(".nodexlm-%x.part", sum[:16]))
}

// Fetch resumes only a representation identified by a strong validator. It
// restarts when Range is ignored, and verifies the full digest after resuming.
// The caller publishes target only after all parts of its model are valid.
func Fetch(ctx context.Context, client *http.Client, source, target string, limit int64, progress func(int64, int64)) (int64, error) {
	select {
	case transfers <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	defer func() { <-transfers }()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if client == nil {
		client = Client()
	}
	if limit < 4 {
		return 0, errors.New("invalid download limit")
	}
	for _, path := range []string{target, target + ".json"} {
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return 0, errors.New("staging path must be a regular file")
		} else if err != nil && !os.IsNotExist(err) {
			return 0, err
		}
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	var previous partial
	if raw, err := os.ReadFile(target + ".json"); err == nil {
		_ = json.Unmarshal(raw, &previous)
	}
	offset := info.Size()
	if previous.Source != source || previous.Validator == "" || offset > limit || previous.Total < offset || previous.Total == offset && previous.SHA256 == "" {
		offset = 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "NodexLM (GGUF downloader)")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		req.Header.Set("If-Range", previous.Validator)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	meta := partial{Source: source, Total: resp.ContentLength}
	// The LFS digest often belongs to the resolve redirect, not its CDN response.
	for current := resp; current != nil; {
		if digest, ok := ExpectedSHA256(current.Header); ok {
			meta.SHA256 = hex.EncodeToString(digest)
		}
		if current.Request == nil {
			break
		}
		current = current.Request.Response
	}
	meta.Validator = resp.Header.Get("ETag")
	if strings.HasPrefix(meta.Validator, "W/") {
		meta.Validator = ""
	}
	if resp.StatusCode == http.StatusPartialContent && offset > 0 {
		var start, end, total int64
		var extra string
		n, _ := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d%s", &start, &end, &total, &extra)
		if n != 3 || start != offset || end < start || total <= end || total > limit || (resp.ContentLength >= 0 && resp.ContentLength != end-start+1) {
			return 0, errors.New("invalid resumed Content-Range")
		}
		if meta.Validator != "" && meta.Validator != previous.Validator {
			return 0, errors.New("download representation changed during resume")
		}
		if previous.SHA256 != "" && meta.SHA256 != "" && previous.SHA256 != meta.SHA256 {
			return 0, errors.New("download digest changed during resume")
		}
		if meta.SHA256 == "" {
			meta.SHA256 = previous.SHA256
		}
		if meta.Validator == "" {
			meta.Validator = previous.Validator
		}
		meta.Total = total
	} else if resp.StatusCode == http.StatusOK {
		offset = 0
		if err := file.Truncate(0); err != nil {
			return 0, err
		}
	} else if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 && previous.Total == offset && resp.Header.Get("Content-Range") == "bytes */"+strconv.FormatInt(offset, 10) && previous.SHA256 != "" {
		meta = previous
	} else {
		return 0, fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	if meta.Total > limit {
		return 0, errors.New("model exceeds download limit")
	}
	if meta.Total >= 0 {
		free, err := FreeBytes(filepath.Dir(target))
		if err != nil {
			return 0, err
		}
		if uint64(max(0, meta.Total-offset)) > free {
			return 0, errors.New("insufficient free disk space for model")
		}
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(target+".json", raw, 0600); err != nil {
		return 0, err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	received := offset
	if progress != nil {
		progress(received, meta.Total)
	}
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		buf := make([]byte, 256<<10)
		for {
			if err := ctx.Err(); err != nil {
				return received, err
			}
			n, readErr := resp.Body.Read(buf)
			if int64(n) > limit-received {
				return received, errors.New("model exceeds download limit")
			}
			if n > 0 {
				if _, err := file.Write(buf[:n]); err != nil {
					return received, err
				}
				received += int64(n)
				if progress != nil {
					progress(received, meta.Total)
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return received, readErr
			}
		}
	}
	if meta.Total >= 0 && received != meta.Total {
		return received, errors.New("incomplete model download")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return received, err
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, &contextReader{ctx: ctx, reader: file}); err != nil {
		return received, err
	}
	if meta.SHA256 != "" && hex.EncodeToString(digest.Sum(nil)) != meta.SHA256 {
		_ = file.Truncate(0)
		_ = os.Remove(target + ".json")
		return received, errors.New("model SHA-256 mismatch")
	}
	var magic [4]byte
	if _, err := file.ReadAt(magic[:], 0); err != nil || string(magic[:]) != "GGUF" {
		return received, errors.New("invalid GGUF shard header")
	}
	return received, file.Sync()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

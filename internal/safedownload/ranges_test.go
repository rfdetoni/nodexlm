package safedownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSegmentedDownloadOverlapsRequests(t *testing.T) {
	t.Setenv("NODEXLM_DOWNLOAD_CONNECTIONS", "4")
	data := bytes.Repeat([]byte("GGUF"), minParallelSize/4)
	tag := fmt.Sprintf("\"%x\"", sha256.Sum256(data))
	var active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", tag)
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
		var start, end int64
		if n, _ := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); n != 2 {
			t.Error("bad range")
			return
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		time.Sleep(20 * time.Millisecond)
		http.ServeContent(w, r, "model.gguf", time.Time{}, bytes.NewReader(data))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "model.part")
	n, err := Fetch(context.Background(), server.Client(), server.URL, path, int64(len(data)), nil)
	if err != nil || n != int64(len(data)) || peak.Load() < 2 {
		t.Fatalf("n=%d peak=%d err=%v", n, peak.Load(), err)
	}
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatal("assembled data differ", err)
	}
}

func TestRangeWindowCongestionAndRecovery(t *testing.T) {
	w := newRangeWindow(8)
	if err := w.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.release(&rangeThrottle{status: 429, delay: 0}, false)
	if w.limit != 2 && w.limit != 1 {
		t.Fatal("congestion must reduce concurrency", w.limit)
	}
	before := w.limit
	for range 8 {
		if err := w.acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
		w.release(nil, false)
	}
	if w.limit != before+1 {
		t.Fatal("successful transfers should cautiously recover capacity", w.limit)
	}
}

func TestSegmentedResumeSkipsCompletedRanges(t *testing.T) {
	t.Setenv("NODEXLM_DOWNLOAD_CONNECTIONS", "2")
	data := bytes.Repeat([]byte("GGUF"), minParallelSize/4)
	tag := fmt.Sprintf("\"%x\"", sha256.Sum256(data))
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", tag)
		rangeHeader := r.Header.Get("Range")
		if rangeHeader != "" && rangeHeader != "bytes=0-0" {
			mu.Lock()
			requests[rangeHeader]++
			mu.Unlock()
		}
		http.ServeContent(w, r, "model.gguf", time.Time{}, bytes.NewReader(data))
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "model.part")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := Fetch(ctx, server.Client(), server.URL, target, int64(len(data)), func(n, _ int64) {
		if n >= rangeSize {
			cancel()
		}
	})
	if err == nil {
		t.Fatal("cancelled transfer must not publish")
	}
	raw, err := os.ReadFile(target + ".ranges.json")
	if err != nil {
		t.Fatal(err)
	}
	var journal rangeJournal
	if err := json.Unmarshal(raw, &journal); err != nil {
		t.Fatal(err)
	}
	var saved []string
	for i, done := range journal.Done {
		if done {
			saved = append(saved, fmt.Sprintf("bytes=%d-%d", i*rangeSize, (i+1)*rangeSize-1))
		}
	}
	if len(saved) == 0 {
		t.Fatal("no durable checkpoint after cancellation")
	}
	mu.Lock()
	requests = map[string]int{}
	mu.Unlock()
	if _, err := Fetch(context.Background(), server.Client(), server.URL, target, int64(len(data)), nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, key := range saved {
		if requests[key] != 0 {
			t.Fatalf("completed range was downloaded again: %s", key)
		}
	}
}

func TestSegmentedFallbackWhenRangeIgnored(t *testing.T) {
	t.Setenv("NODEXLM_DOWNLOAD_CONNECTIONS", "4")
	data := bytes.Repeat([]byte("GGUF"), minParallelSize/4)
	tag := fmt.Sprintf("\"%x\"", sha256.Sum256(data))
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", tag)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		if r.Header.Get("Range") != "" {
			probes.Add(1)
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "model.part")
	n, err := Fetch(context.Background(), server.Client(), server.URL, target, int64(len(data)), nil)
	if err != nil || n != int64(len(data)) || probes.Load() != 1 {
		t.Fatal(n, probes.Load(), err)
	}
	if _, err := os.Stat(target + ".ranges"); !os.IsNotExist(err) {
		t.Fatal("unsupported range should not allocate parallel staging", err)
	}
}

func TestRangeRejectsChangedValidator(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"changed"`)
		w.Header().Set("Content-Range", "bytes 0-0/10")
		w.WriteHeader(206)
		_, _ = w.Write([]byte("G"))
	}))
	defer server.Close()
	resp, err := requestRange(context.Background(), server.Client(), server.URL, partial{Validator: `"original"`, Total: 10}, 0, 1)
	if err == nil {
		resp.Body.Close()
		t.Fatal("changed validator accepted")
	}
}

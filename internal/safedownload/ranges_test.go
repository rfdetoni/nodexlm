package safedownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

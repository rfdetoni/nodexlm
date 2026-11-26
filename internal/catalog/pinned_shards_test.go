package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPinnedShardsOverlapAndPublish(t *testing.T) {
	t.Setenv("NODEXLM_DOWNLOAD_CONNECTIONS", "4")
	revision := strings.Repeat("a", 40)
	var active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Repo-Commit", revision)
		body := "GGUF" + filepath.Base(r.URL.Path)
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if r.Method == http.MethodHead {
			return
		}
		if !strings.Contains(r.URL.Path, "/resolve/"+revision+"/") {
			t.Error("download not pinned")
			w.WriteHeader(400)
			return
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	hub := Hub{Client: &http.Client{Transport: rewriteTransport{endpoint: server.URL, base: http.DefaultTransport}}}
	dir := t.TempDir()
	first, err := hub.DownloadFile(context.Background(), "owner/repo", "model-00001-of-00002.gguf", dir, "", nil)
	if err != nil || peak.Load() < 2 {
		t.Fatal(first, peak.Load(), err)
	}
	for _, name := range []string{"model-00001-of-00002.gguf", "model-00002-of-00002.gguf"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != "GGUF"+name {
			t.Fatal(name, err)
		}
	}
}

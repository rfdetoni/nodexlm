package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelURLRejectsUnsafePaths(t *testing.T) {
	for _, x := range [][2]string{{"bad", "model.gguf"}, {"o/r", "../evil.gguf"}, {"o/r", "a/../../b.gguf"}, {"o/r", "readme.md"}, {"o/r", "x\\y.gguf"}} {
		if _, err := ModelURL(x[0], x[1]); err == nil {
			t.Errorf("accepted unsafe path %v", x)
		}
	}
	u, err := ModelURL("Qwen/Qwen-GGUF", "Q4_K_M/model.gguf")
	if err != nil || !strings.Contains(u, "/resolve/main/Q4_K_M/model.gguf") {
		t.Fatal(u, err)
	}
}
func TestDownloadFileStreamAndValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/owner/repo/resolve/main/file.gguf" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("GGUF"))
		w.Write([]byte("test payload"))
	}))
	defer server.Close()
	h := Hub{Client: &http.Client{Transport: rewriteTransport{endpoint: server.URL, base: http.DefaultTransport}}}
	dir := t.TempDir()
	var total int64
	path, err := h.DownloadFile(context.Background(), "owner/repo", "file.gguf", dir, "", func(n, all int64) { total = n })
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "GGUFtest payload" || total != int64(len(b)) {
		t.Fatal(err, total, string(b))
	}
	if _, err = h.DownloadFile(context.Background(), "owner/repo", "file.gguf", dir, "", nil); err == nil {
		t.Fatal("overwritten existing file")
	}
}
func TestManualImport(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "old.gguf")
	os.WriteFile(source, []byte("GGUFabc"), 0600)
	dst, err := ImportFile(context.Background(), source, filepath.Join(dir, "managed"), "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "GGUFabc" {
		t.Fatal(err)
	}
	os.WriteFile(source, []byte("garbage"), 0600)
	if _, err = ImportFile(context.Background(), source, filepath.Join(dir, "other"), ""); err == nil {
		t.Fatal("accepted invalid file")
	}
}

type rewriteTransport struct {
	endpoint string
	base     http.RoundTripper
}

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	cloned := r.Clone(r.Context())
	cloned.URL.Scheme = "http"
	cloned.URL.Host = strings.TrimPrefix(t.endpoint, "http://")
	return t.base.RoundTrip(cloned)
}

func TestShardNamesBoundAndCompleteness(t *testing.T) {
	names, err := ShardNames("model-00001-of-00003.gguf")
	if err != nil || len(names) != 3 || names[1] != "model-00002-of-00003.gguf" {
		t.Fatalf("shards=%v err=%v", names, err)
	}
	for _, name := range []string{"model-00002-of-00003.gguf", "model-00001-of-00129.gguf", "model-00001-of-00001.gguf"} {
		if _, err := ShardNames(name); err == nil {
			t.Fatalf("accepted invalid first shard %q", name)
		}
	}
}
func TestCatalogIncludesOnlyCompleteShardSets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/models" {
			w.Write([]byte(`[{"id":"owner/repo","gated":false}]`))
			return
		}
		if r.URL.Path == "/api/models/owner/repo/tree/main" {
			w.Write([]byte(`[
				{"type":"file","path":"good-00001-of-00002.gguf","size":100},
				{"type":"file","path":"good-00002-of-00002.gguf","size":200},
				{"type":"file","path":"missing-00001-of-00002.gguf","size":100}
			]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	models, err := (Hub{APIBase: server.URL}).Search(context.Background(), "qwen")
	if err != nil || len(models) != 1 {
		t.Fatalf("models=%v err=%v", models, err)
	}
	if models[0].Filename != "good-00001-of-00002.gguf" || models[0].SizeBytes != 300 || models[0].ShardCount != 2 {
		t.Fatalf("incorrect shard grouping: %+v", models[0])
	}
}


func TestDownloadAndImportCompleteShardedSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "model-00001-of-00002.gguf", "model-00002-of-00002.gguf":
			_, _ = w.Write([]byte("GGUF" + filepath.Base(r.URL.Path)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	hub := Hub{Client: &http.Client{Transport: rewriteTransport{endpoint: server.URL, base: http.DefaultTransport}}}
	dir := t.TempDir()
	first, err := hub.DownloadFile(context.Background(), "owner/repo",
		"quant/model-00001-of-00002.gguf", dir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "model-00002-of-00002.gguf")
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("missing second downloaded shard: %v", err)
	}
	importedDir := filepath.Join(t.TempDir(), "models")
	copied, err := ImportFile(context.Background(), first, importedDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(importedDir, "model-00002-of-00002.gguf")); err != nil {
		t.Fatalf("missing imported shard: %v", err)
	}
	if filepath.Base(copied) != filepath.Base(first) {
		t.Fatalf("unexpected import destination: %s", copied)
	}
}

func TestShardedTransferRollsBackAfterMissingPart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "model-00001-of-00002.gguf") {
			_, _ = w.Write([]byte("GGUFfirst"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	hub := Hub{Client: &http.Client{Transport: rewriteTransport{endpoint: server.URL, base: http.DefaultTransport}}}
	dir := t.TempDir()
	if _, err := hub.DownloadFile(context.Background(), "owner/repo",
		"model-00001-of-00002.gguf", dir, "", nil); err == nil {
		t.Fatal("accepted incomplete sharded download")
	}
	if _, err := os.Stat(filepath.Join(dir, "model-00001-of-00002.gguf")); !os.IsNotExist(err) {
		t.Fatalf("partial downloaded model was published: %v", err)
	}
	source := filepath.Join(t.TempDir(), "model-00001-of-00002.gguf")
	if err := os.WriteFile(source, []byte("GGUFfirst"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportFile(context.Background(), source, dir, ""); err == nil {
		t.Fatal("accepted incomplete manual import")
	}
	if _, err := os.Stat(filepath.Join(dir, "model-00001-of-00002.gguf")); !os.IsNotExist(err) {
		t.Fatalf("partial manual import was published: %v", err)
	}
}

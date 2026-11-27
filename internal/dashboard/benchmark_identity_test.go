package dashboard

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestBenchmarkHashPart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.gguf")
	data := []byte("GGUF test bytes")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	part, err := benchmarkHashPart(context.Background(), path, make([]byte, 128))
	if err != nil || part["sha256"] != fmt.Sprintf("%x", sha256.Sum256(data)) || part["size_bytes"] != int64(len(data)) {
		t.Fatalf("unexpected model identity: %+v %v", part, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := benchmarkHashPart(ctx, path, make([]byte, 128)); err == nil {
		t.Fatal("cancelled model hash must not succeed")
	}
}

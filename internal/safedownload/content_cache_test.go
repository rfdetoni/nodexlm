package safedownload

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifiedPublicationDeduplicatesAndRejectsStaleIndex(t *testing.T) {
	t.Setenv("NODEXLM_MODEL_DEDUP", "1")
	root := t.TempDir()
	ctx := WithContentCache(context.Background(), root)
	data := []byte("GGUF identical weights")
	stage := filepath.Join(root, ".part")
	first := filepath.Join(root, "first.gguf")
	second := filepath.Join(root, "second.gguf")
	for _, p := range []string{stage, first} {
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	meta := partial{SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Total: int64(len(data))}
	raw, _ := json.Marshal(meta)
	if err := os.WriteFile(stage+".json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	RememberPublished(ctx, stage, first)
	ok, err := PublishVerifiedLink(ctx, stage, second)
	if err != nil || !ok {
		t.Skipf("hard links unavailable: %v", err)
	}
	a, _ := os.Stat(first)
	b, _ := os.Stat(second)
	if !os.SameFile(a, b) {
		t.Fatal("identical model must reuse the inode")
	}
	if err := os.WriteFile(first, []byte("GGUF modified weights!"), 0600); err != nil {
		t.Fatal(err)
	}
	ok, err = PublishVerifiedLink(ctx, stage, filepath.Join(root, "third.gguf"))
	if err != nil || ok {
		t.Fatal("stale content cache accepted", err)
	}
}

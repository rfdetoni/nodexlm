package safedownload

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
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

func TestColdModelVerificationHandlesConcurrentRangeReaders(t *testing.T) {
	t.Setenv("NODEXLM_MODEL_P2P", "1")
	root := t.TempDir()
	body := make([]byte, 1024*1024)
	copy(body, []byte("GGUF"))
	sha := fmt.Sprintf("%x", sha256.Sum256(body))
	if err := os.WriteFile(filepath.Join(root, "weights.gguf"), body, 0600); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(root, ".nodexlm-content-index")
	if err := os.Mkdir(index, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(index, sha), []byte("weights.gguf"), 0600); err != nil {
		t.Fatal(err)
	}
	// Simulate the first four parallel cold GETs plus a second waiting batch.
	// They must validate without data races or unbounded verification state.
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			file, err := OpenVerifiedContent(context.Background(), root, sha)
			if err != nil {
				failures <- err
				return
			}
			defer file.Close()
			var prefix [4]byte
			if _, err := io.ReadFull(file, prefix[:]); err != nil {
				failures <- err
			} else if string(prefix[:]) != "GGUF" {
				failures <- fmt.Errorf("unexpected model signature %q", prefix)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
}

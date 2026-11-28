package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureTrellisPLERejectsUnverifiedExistingFile(t *testing.T) {
	dir := t.TempDir()
	model := filepath.Join(dir, "Qwen3.8-Flash-Next-Gyro-noPLE.gguf")
	sidecar := filepath.Join(dir, flashNextPLE)
	// Structurally valid GGUF but not the approved PLE table.
	if err := os.WriteFile(sidecar, ggufTestFile("qwen4exp", 0, 0), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureTrellisPLE(context.Background(), model, GGUFInfo{Architecture: "qwen4exp"}, nil); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("unverified PLE accepted: %v", err)
	}
}

func TestVerifyCachedTrellisPLERejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, flashNextPLE)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := verifyCachedTrellisPLE(context.Background(), link); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symbolic link accepted: %v", err)
	}
}

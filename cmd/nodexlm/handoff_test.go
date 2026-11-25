// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHandoffVerifiedShardsAndRollback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	first := filepath.Join(root, "model-00001-of-00002.gguf")
	second := filepath.Join(root, "model-00002-of-00002.gguf")
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, []byte("GGUFweights"), 0600); err != nil { t.Fatal(err) }
	}
	m, err := makeHandoffManifest(ctx, first)
	if err != nil || len(m.Parts) != 2 { t.Fatalf("manifest=%+v err=%v", m, err) }
	target := filepath.Join(root, "new-host")
	if err := stageHandoff(ctx, first, target, m); err != nil { t.Fatal(err) }
	for _, p := range m.Parts {
		got, err := hashHandoffPart(ctx, filepath.Join(target, "models", p.Name))
		if err != nil || got != p { t.Fatalf("part=%+v err=%v", got, err) }
	}
	if err := stageHandoff(ctx, first, target, m); err == nil { t.Fatal("overwrote existing host") }
	if err := os.WriteFile(second, []byte("GGUFchanges"), 0600); err != nil { t.Fatal(err) }
	failed := filepath.Join(root, "failed-host")
	if err := stageHandoff(ctx, first, failed, m); err == nil { t.Fatal("accepted corrupted shard") }
	if _, err := os.Stat(failed); !os.IsNotExist(err) { t.Fatalf("partial destination remains: %v", err) }
	if _, err := os.Stat(first); err != nil { t.Fatal("source was removed", err) }
}

func TestHandoffRejectsUntrustedManifest(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "model.gguf")
	if err := os.WriteFile(source, []byte("GGUFdata"), 0600); err != nil { t.Fatal(err) }
	m, err := makeHandoffManifest(context.Background(), source)
	if err != nil { t.Fatal(err) }
	m.Parts[0].Name = "../model.gguf"
	if _, err := validateHandoffManifest(m); err == nil { t.Fatal("accepted traversal") }
	m.Parts[0].Name = "model.gguf"
	m.Parts[0].SHA256 = "invalid"
	if _, err := validateHandoffManifest(m); err == nil { t.Fatal("accepted invalid hash") }
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedModelDeletionRequiresAdminAndRejectsActiveLoads(t *testing.T) {
	h, m := newTestUI(t)
	key := strings.Repeat("a", 64)
	path := filepath.Join(m.modelDir, "sample.gguf")
	if err := os.WriteFile(path, []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := req(h, http.MethodDelete, "/api/models/sample.gguf", "", "", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized deletion: %d", got)
	}
	if got := req(h, http.MethodGet, "/api/models/folder", key, "", nil); got.Code != http.StatusOK {
		t.Fatalf("folder path unavailable: %d", got.Code)
	} else {
		var folder map[string]string
		if err := json.Unmarshal(got.Body.Bytes(), &folder); err != nil || folder["path"] != m.ModelFolder() {
			t.Fatalf("unexpected Host folder: %v (%v)", folder, err)
		}
	}
	m.mu.Lock()
	m.loading = true
	m.mu.Unlock()
	if got := req(h, http.MethodDelete, "/api/models/sample.gguf", key, "", nil).Code; got != http.StatusConflict {
		t.Fatalf("deleted model during loading: %d", got)
	}
	m.mu.Lock()
	m.loading = false
	m.mu.Unlock()
	settings := m.Settings()
	settings.Model = "sample.gguf"
	if err := m.Update(settings); err != nil {
		t.Fatal(err)
	}
	if got := req(h, http.MethodDelete, "/api/models/sample.gguf", key, "", nil).Code; got != http.StatusOK {
		t.Fatalf("model deletion failed: %d", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("model still exists: %v", err)
	}
	if m.Settings().Model != "" {
		t.Fatal("deleted model was still selected")
	}
}

func TestShardedDeletionAndSymlinkRejection(t *testing.T) {
	_, m := newTestUI(t)
	shards := []string{"test-00001-of-00002.gguf", "test-00002-of-00002.gguf"}
	for _, name := range shards {
		if err := os.WriteFile(filepath.Join(m.modelDir, name), []byte("GGUF"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.DeleteModel(shards[0]); err != nil {
		t.Fatal(err)
	}
	for _, name := range shards {
		if _, err := os.Stat(filepath.Join(m.modelDir, name)); !os.IsNotExist(err) {
			t.Fatalf("shard %s remains: %v", name, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.gguf")
	if err := os.WriteFile(outside, []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(m.modelDir, "outside.gguf")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if err := m.DeleteModel("outside.gguf"); err == nil {
		t.Fatal("model deletion followed a symlink")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("external file was touched: %v", err)
	}
}

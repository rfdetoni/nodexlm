// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/coordinator"
)

func newTestUI(t *testing.T) (http.Handler, *Manager) {
	t.Helper()
	dir := t.TempDir()
	m, e := NewManager(dir, filepath.Join(dir, "models"), "llama-server")
	if e != nil {
		t.Fatal(e)
	}
	s, e := coordinator.New(strings.Repeat("a", 64), strings.Repeat("b", 64))
	if e != nil {
		t.Fatal(e)
	}
	return New(m, s, strings.Repeat("a", 64)).Handler(), m
}
func req(h http.Handler, method, path, auth, origin string, payload any) *httptest.ResponseRecorder {
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Host = "127.0.0.1:8787"
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if payload != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestDashboardLocalAndAuthorization(t *testing.T) {
	h, _ := newTestUI(t)
	secret := strings.Repeat("a", 64)
	if got := req(h, "GET", "/api/status", "", "", nil).Code; got != 401 {
		t.Fatalf("without token: %d", got)
	}
	if got := req(h, "GET", "/api/status", strings.Repeat("x", 64), "", nil).Code; got != 401 {
		t.Fatalf("invalid token: %d", got)
	}
	if got := req(h, "GET", "/api/status", secret, "", nil).Code; got != 200 {
		t.Fatalf("admin token: %d", got)
	}
	if got := req(h, "GET", "/", "", "", nil).Code; got != 200 {
		t.Fatalf("embedded UI: %d", got)
	}
	if got := req(h, "GET", "/api/status", secret, "http://attacker.example", nil).Code; got != 403 {
		t.Fatalf("cross-origin allowed: %d", got)
	}
	r := httptest.NewRequest("GET", "http://evil.example/api/status", nil)
	r.Host = "evil.example"
	r.Header.Set("Authorization", "Bearer "+secret)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("DNS rebinding host allowed: %d", w.Code)
	}
}
func TestSettingsPersistenceAndGuards(t *testing.T) {
	h, m := newTestUI(t)
	secret := strings.Repeat("a", 64)
	s := Settings{Model: "demo.gguf", Threads: 2, ContextTokens: 2048, GPULayers: "auto"}
	if got := req(h, "PUT", "/api/settings", secret, "", s).Code; got != 200 {
		t.Fatalf("valid settings: %d", got)
	}
	raw, e := os.ReadFile(filepath.Join(m.dir, "runtime.json"))
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(raw, []byte("demo.gguf")) {
		t.Fatalf("settings not persisted: %s", raw)
	}
	s.Model = "../evil.gguf"
	if got := req(h, "PUT", "/api/settings", secret, "", s).Code; got != 400 {
		t.Fatalf("path traversal allowed: %d", got)
	}
	s.Model = "demo.gguf"
	s.MoECPU = true
	s.MoECPULayers = 2
	if got := req(h, "PUT", "/api/settings", secret, "", s).Code; got != 400 {
		t.Fatalf("conflicting MoE allowed: %d", got)
	}
	if got := req(h, "POST", "/api/download", secret, "", map[string]string{"url": "http://127.0.0.1/evil.gguf", "name": "evil.gguf"}).Code; got != 400 {
		t.Fatalf("SSRF allowed: %d", got)
	}
	if got := req(h, "POST", "/api/download", secret, "", map[string]string{"url": "https://huggingface.co/x/y/resolve/main/test.gguf", "name": "../../x.gguf"}).Code; got != 400 {
		t.Fatalf("traversal allowed: %d", got)
	}
}
func TestModelSymlinkRejected(t *testing.T) {
	_, m := newTestUI(t)
	outside := filepath.Join(t.TempDir(), "secret.gguf")
	if err := os.WriteFile(outside, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(m.modelDir, "escape.gguf")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := modelPath(m.modelDir, "escape.gguf"); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestURLRestrictions(t *testing.T) {
	for _, tc := range []string{"https://localhost/x.gguf", "http://huggingface.co/a/b.gguf", "https://huggingface.co:8443/a.gguf", "https://user:pass@huggingface.co/a.gguf", "https://huggingface.co/a.bin"} {
		if _, err := parseModelURL(tc); err == nil {
			t.Fatalf("accepted %q", tc)
		}
	}
	if _, err := parseModelURL("https://huggingface.co/owner/repo/resolve/main/model.gguf?download=1"); err != nil {
		t.Fatal(err)
	}
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/coordinator"
	"github.com/rfdetoni/nodexlm/internal/local"
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

func TestAsyncLoadDoesNotBlockStatusAndReportsError(t *testing.T) {
	h, m := newTestUI(t)
	const secret = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// Distributed mode without an RPC provider fails before runtime download.
	if err := os.WriteFile(filepath.Join(m.modelDir, "test.gguf"), []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := Settings{Model: "test.gguf", Threads: 2, ContextTokens: 2048, GPULayers: "auto", Distributed: true}
	if got := req(h, "PUT", "/api/settings", secret, "", settings).Code; got != 200 {
		t.Fatalf("settings: HTTP %d", got)
	}
	if got := req(h, "POST", "/api/load", secret, "", nil).Code; got != 202 {
		t.Fatalf("load should be asynchronous: HTTP %d", got)
	}
	for deadline := time.Now().Add(2 * time.Second); ; {
		started := time.Now()
		status := req(h, "GET", "/api/status", secret, "", nil)
		if status.Code != 200 || time.Since(started) > time.Second {
			t.Fatalf("status blocked or unavailable while preparing: %d", status.Code)
		}
		var payload struct {
			Runtime struct {
				Loading bool   `json:"loading"`
				Phase   string `json:"phase"`
				Error   string `json:"error"`
			} `json:"runtime"`
		}
		if err := json.Unmarshal(status.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if !payload.Runtime.Loading {
			if payload.Runtime.Phase != "error" || payload.Runtime.Error == "" {
				t.Fatalf("async error not reflected in status: %+v", payload.Runtime)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("model load did not report failure")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInferenceTimeoutFollowsDistributedSetting(t *testing.T) {
	_, m := newTestUI(t)
	if got := m.inferenceTimeout(""); got != 4*time.Minute {
		t.Fatalf("local inference timeout = %v", got)
	}
	s := Settings{Threads: 2, ContextTokens: 2048, GPULayers: "auto", Distributed: true}
	if err := m.Update(s); err != nil {
		t.Fatal(err)
	}
	if got := m.inferenceTimeout(""); got != 4*time.Minute {
		t.Fatalf("saved distribution preference must not extend local timeout: %v", got)
	}
	m.mu.Lock()
	m.engine = &local.Managed{RPCServers: []string{"127.0.0.1:50052"}}
	m.current = "remote.gguf"
	m.mu.Unlock()
	if got := m.inferenceTimeout("remote.gguf"); got != 20*time.Minute {
		t.Fatalf("active distributed timeout = %v", got)
	}
	if got := m.inferenceTimeout("resident.gguf"); got != 4*time.Minute {
		t.Fatalf("local resident timeout = %v", got)
	}
}

func TestAdminAndInferenceCredentialsStaySeparated(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir, filepath.Join(dir, "models"), "llama-server")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	adminToken, inferenceToken := strings.Repeat("a", 64), strings.Repeat("c", 64)
	s, err := coordinator.New(adminToken, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseRPC()
	d := New(m, s, adminToken)
	if err := d.SetAPIKey(inferenceToken); err != nil {
		t.Fatal(err)
	}
	h := d.Handler()
	for _, tc := range []struct {
		path, key string
		want      int
	}{
		{"/api/status", adminToken, 200},
		{"/api/status", inferenceToken, 401},
		{"/v1/models", inferenceToken, 200},
		{"/v1/models", adminToken, 200},
		{"/v1/models", strings.Repeat("z", 64), 401},
	} {
		if got := req(h, http.MethodGet, tc.path, tc.key, "", nil).Code; got != tc.want {
			t.Errorf("%s with key prefix %q: got %d, want %d", tc.path, tc.key[:2], got, tc.want)
		}
	}
}

func TestCancelPrimaryLoadDoesNotCancelPoolWork(t *testing.T) {
	h, m := newTestUI(t)
	defer m.Close()
	secret := strings.Repeat("a", 64)
	path := filepath.Join(m.modelDir, "cancel.gguf")
	if err := os.WriteFile(path, []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Update(Settings{Model: "cancel.gguf", Threads: 2, ContextTokens: 2048, GPULayers: "auto", Distributed: true}); err != nil {
		t.Fatal(err)
	}
	poolCtx, poolCancel := context.WithCancel(context.Background())
	defer poolCancel()
	m.poolMu.Lock()
	m.mu.Lock()
	m.poolCancel = poolCancel
	m.mu.Unlock()
	if got := req(h, "POST", "/api/load", secret, "", nil).Code; got != 202 {
		m.poolMu.Unlock()
		t.Fatalf("load: %d", got)
	}
	if got := req(h, "POST", "/api/inference/cancel-load", "", "", nil).Code; got != 401 {
		m.poolMu.Unlock()
		t.Fatalf("unauthenticated cancel: %d", got)
	}
	for range 2 {
		if got := req(h, "POST", "/api/inference/cancel-load", secret, "", nil).Code; got != 200 {
			m.poolMu.Unlock()
			t.Fatalf("cancel: %d", got)
		}
	}
	if poolCtx.Err() != nil || m.State()["phase"] != "cancelling" {
		m.poolMu.Unlock()
		t.Fatal("cancel touched unrelated pool operation or lost cancelling state")
	}
	m.poolMu.Unlock()
	for deadline := time.Now().Add(2 * time.Second); ; {
		state := m.State()
		if state["loading"] == false {
			if state["phase"] != "cancelled" || state["loaded"] != false || state["error"] != "" {
				t.Fatalf("cancelled load published state: %+v", state)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled load retained the pool")
		}
		time.Sleep(time.Millisecond)
	}
	if m.CancelLoad() || poolCtx.Err() != nil || len(m.LoadedModels()) != 0 || len(m.InferenceSamples()) != 0 {
		t.Fatal("cancellation changed unrelated work or published a model/sample")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("model file removed:", err)
	}
	m.Unload()
	if m.State()["phase"] != "idle" {
		t.Fatal("cancelled state did not return to idle")
	}
	if err := m.StartLoad(); err != nil {
		t.Fatal("subsequent load was rejected:", err)
	}
}

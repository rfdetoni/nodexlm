package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllayaLoopbackURLGuard(t *testing.T) {
	for _, v := range []string{
		"https://127.0.0.1:11435", "http://example.com:11435",
		"http://localhost:11435", "http://169.254.169.254/",
		"http://127.0.0.1:11435/api", "http://127.0.0.1:11435?x=1",
		"http://user:pw@127.0.0.1:11435",
	} {
		if _, err := decisionBackendURL(v); err == nil {
			t.Errorf("unsafe Ollaya origin accepted: %q", v)
		}
	}
	for _, v := range []string{"http://127.0.0.1:11435", "http://[::1]:11435/"} {
		if _, err := decisionBackendURL(v); err != nil {
			t.Errorf("safe Ollaya origin rejected: %q: %v", v, err)
		}
	}
}

func TestOllayaSystemOneProxyIsAuthenticatedAndIsolated(t *testing.T) {
	var seen int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("wrong upstream route: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-only" {
			t.Errorf("NodeXLM key leaked / wrong upstream token: %q", got)
		}
		var body struct {
			Model string `json:"model"`
			State string `json:"state"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "laya:multilingual" || body.State != "Urgent refund" {
			t.Errorf("invalid upstream payload: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-typesafe-request-id", "ollaya-req")
		_, _ = w.Write([]byte(`{"answers":{"urgent":{"noul":0.99}}}`))
	}))
	defer upstream.Close()
	t.Setenv("NODEXLM_OLLAYA_URL", upstream.URL)
	t.Setenv("NODEXLM_OLLAYA_API_KEY", "upstream-only")
	h, m := newTestUI(t)
	_ = m
	admin := strings.Repeat("a", 64)
	payload := map[string]any{
		"model":     "ollaya/laya:multilingual",
		"state":     "Urgent refund",
		"questions": map[string]any{"urgent": map[string]any{"type": "noul", "instructions": "Is it urgent?"}},
	}
	w := req(h, http.MethodPost, "/v1/systemone", admin, "", payload)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"noul":0.99`) {
		t.Fatalf("decision request: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("x-typesafe-request-id") != "ollaya-req" {
		t.Fatal("decision tracing header was lost")
	}
	if got := req(h, http.MethodPost, "/v1/systemone", "wrong", "", payload).Code; got != 401 {
		t.Fatalf("unguarded decision endpoint: %d", got)
	}
	if seen != 1 {
		t.Fatalf("unexpected upstream request count: %d", seen)
	}
}

func TestOllayaDecisionFailsClosed(t *testing.T) {
	h, _ := newTestUI(t)
	token := strings.Repeat("a", 64)
	payload := map[string]any{
		"model": "ollaya/laya:en", "state": "hi",
		"questions": map[string]any{"a": map[string]any{"type": "noul", "instructions": "Is it a greeting?"}},
	}
	t.Setenv("NODEXLM_OLLAYA_URL", "")
	if got := req(h, "POST", "/v1/systemone", token, "", payload).Code; got != 503 {
		t.Fatalf("unconfigured decision runtime: %d", got)
	}
	t.Setenv("NODEXLM_OLLAYA_URL", "http://example.com:80")
	if got := req(h, "POST", "/v1/systemone", token, "", payload).Code; got != 503 {
		t.Fatalf("remote decision origin allowed: %d", got)
	}
	payload["model"] = "unloaded.gguf"
	if got := req(h, "POST", "/v1/systemone", token, "", payload).Code; got != 404 {
		t.Fatalf("unloaded GGUF accepted: %d", got)
	}
}

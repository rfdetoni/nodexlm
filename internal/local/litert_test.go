package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestLiteRTRegistryAndChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gemma"}]}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	engine, err := NewLiteRT(ctx, server.URL, "gemma")
	if err != nil {
		t.Fatal(err)
	}
	defer engine.HTTP.CloseIdleConnections()
	answer, err := engine.Generate(ctx, protocol.ChatRequest{Model: "gemma", Messages: []protocol.Message{{Role: "user", Content: "hello"}}})
	if err != nil || answer != "ok" {
		t.Fatalf("%q %v", answer, err)
	}
	if _, err := NewLiteRT(ctx, server.URL, "missing"); err == nil {
		t.Fatal("accepted absent registry model")
	}
	if _, err := NewLiteRT(ctx, "http://192.168.1.1:9379", "gemma"); err == nil {
		t.Fatal("accepted non-loopback endpoint")
	}
}

func TestRegisteredRuntimeRejectsRedirectAndUnlistedModel(t *testing.T) {
	t.Setenv("NODEXLM_RUNTIME_API_KEY", "local-test-key")
	var redirected bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected = true
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"model"}]}`))
			return
		}
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	ctx := context.Background()
	for _, id := range []string{"ollama", "vllm", "sglang", "mlx-lm", "tabbyapi", "openai"} {
		engine, err := NewRegisteredRuntime(ctx, id, server.URL, "model")
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if _, err := engine.Generate(ctx, protocol.ChatRequest{Model: "model"}); err == nil {
			t.Fatal("runtime redirect was accepted")
		}
		engine.HTTP.CloseIdleConnections()
	}
	if redirected {
		t.Fatal("redirect reached another endpoint")
	}
	for _, endpoint := range []string{"http://localhost:1234", server.URL + "/other", server.URL + "?token=x"} {
		if _, err := NewRegisteredRuntime(ctx, "ollama", endpoint, "model"); err == nil {
			t.Fatalf("accepted invalid origin %q", endpoint)
		}
	}
	if _, err := NewRegisteredRuntime(ctx, "ollama", server.URL, "missing"); err == nil {
		t.Fatal("accepted absent registry model")
	}
}

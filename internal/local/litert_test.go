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

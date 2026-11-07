// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestEngineAndGGUFScanner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("bad path %s", r.URL.Path)
		}
		var input protocol.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input.Messages) != 1 || input.Messages[0].Content != "hello" {
			t.Errorf("bad payload: %+v", input)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": protocol.Message{Role: "assistant", Content: "world"}}}})
	}))
	defer server.Close()
	e, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.Generate(context.Background(), protocol.ChatRequest{Model: "m", Messages: []protocol.Message{{Role: "user", Content: "hello"}}})
	if err != nil || out != "world" {
		t.Fatalf("out %q err %v", out, err)
	}
	if _, err := New("http://192.168.0.5:8080"); err == nil {
		t.Fatal("remote backend allowed")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "valid.gguf"), ggufTestFile("llama", 0, 0), 0600)
	_ = os.WriteFile(filepath.Join(dir, "fake.gguf"), []byte("NOT A GGUF"), 0600)
	models, err := Models(dir)
	if err != nil || len(models) != 1 || models[0].Name != "valid.gguf" {
		t.Fatalf("models %v err %v", models, err)
	}
}

func TestRPCArgsDoNotExposeExternalEndpoints(t *testing.T) {
	m := &Managed{ModelPath: "model.gguf", Alias: "model.gguf", RPCServers: []string{"127.0.0.1:50051", "127.0.0.1:50052"}}
	args := m.args(17000, 2048, 4, "all", 32)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--rpc 127.0.0.1:50051,127.0.0.1:50052") || !strings.Contains(joined, "--split-mode layer") || !strings.Contains(joined, "--gpu-layers 33") || !strings.Contains(joined, "--override-tensor") || !strings.Contains(joined, "--no-op-offload") {
		t.Fatalf("missing distributed parameters: %v", args)
	}
	m.RPCServers = []string{"192.168.1.5:50052"}
	args = m.args(17000, 2048, 4, "all", 32)
	if strings.Contains(strings.Join(args, " "), "--rpc") {
		t.Fatal("nonloopback RPC endpoint must not be forwarded")
	}
}

func TestRPCFirstLayersStayLocal(t *testing.T) {
	m := &Managed{ModelPath: "model.gguf", Alias: "model", RPCServers: []string{"127.0.0.1:50052"}, RPCLocalLayers: 12}
	args := strings.Join(m.args(17000, 4096, 8, "auto", 40), " ")
	if !strings.Contains(args, "--gpu-layers 29") {
		t.Fatalf("expected 28 remote layers plus the locally overridden output tensor; got %s", args)
	}
}

func TestEngineSendsManagedBearerCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer per-process-secret" {
			t.Errorf("missing managed inference authorization: %q", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{
			map[string]any{"message": protocol.Message{Role: "assistant", Content: "ok"}},
		}})
	}))
	defer server.Close()
	engine, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	engine.APIKey = "per-process-secret"
	out, err := engine.Generate(context.Background(), protocol.ChatRequest{Messages: []protocol.Message{{Role: "user", Content: "hello"}}})
	if err != nil || out != "ok" {
		t.Fatalf("authenticated generation: output %q err %v", out, err)
	}
}

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

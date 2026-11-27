package dashboard

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBenchmarkStreamRequiresAdminAndValidInput(t *testing.T) {
	h, m := newTestUI(t)
	defer m.Close()
	input := map[string]any{"action": "single", "model": "missing.gguf"}
	if got := req(h, "POST", "/api/bench/stream", "", "", input).Code; got != http.StatusUnauthorized {
		t.Fatalf("anonymous benchmark stream: %d", got)
	}
	secret := strings.Repeat("a", 64)
	if got := req(h, "POST", "/api/bench/stream", secret, "", map[string]any{"action": "series", "rounds": 11}).Code; got != http.StatusBadRequest {
		t.Fatalf("invalid rounds: %d", got)
	}
	w := req(h, "POST", "/api/bench/stream", secret, "", input)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("not a stream: %d %s", w.Code, w.Body.String())
	}
	decoder := json.NewDecoder(w.Body)
	var phases []string
	var last benchmarkProgressEvent
	for decoder.More() {
		var event benchmarkProgressEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "progress" {
			phases = append(phases, event.Phase)
		}
		last = event
	}
	if last.Type != "error" || last.Error == "" || len(phases) < 3 || phases[0] != "prepare" {
		t.Fatalf("expected real progress and terminal error: %v %+v", phases, last)
	}
}

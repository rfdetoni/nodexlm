package local

import (
	"bytes"
	"encoding/json"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultLlamaBackendCompatibility(t *testing.T) {
	if EffectiveBackend("") != BackendLlama {
		t.Fatal("empty runtime must preserve llama.cpp")
	}
	m := &Managed{ModelPath: "/tmp/model.gguf", Alias: "demo", Threads: 3, ContextTokens: 1024, GPULayers: "auto"}
	spec, err := m.backendLaunch(12345, 1024, 3, "auto", GGUFInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if spec.ReadyPath != "/health" || spec.APIKey == "" || spec.Timeout <= 0 {
		t.Fatalf("llama executable contract changed: %+v", spec)
	}
	if !reflect.DeepEqual(spec.Args[:2], []string{"--host", "127.0.0.1"}) {
		t.Fatalf("llama launch flags changed: %v", spec.Args)
	}
}

func TestMistralRSDisabledByDefault(t *testing.T) {
	t.Setenv("NODEXLM_EXPERIMENTAL_MISTRALRS", "")
	m := &Managed{Backend: BackendMistralRS, ModelPath: "/tmp/model.gguf"}
	if _, err := m.backendLaunch(12345, 1024, 1, "auto", GGUFInfo{}); err == nil ||
		!strings.Contains(err.Error(), "EXPERIMENTAL") {
		t.Fatalf("expected explicit opt-in, got %v", err)
	}
}

func TestMistralRSLaunchIsLoopbackOnly(t *testing.T) {
	t.Setenv("NODEXLM_EXPERIMENTAL_MISTRALRS", "1")
	// The test runner is a real executable; do not start it. This checks only CLI.
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODEXLM_MISTRALRS_BIN", bin)
	m := &Managed{Backend: BackendMistralRS, ModelPath: filepath.Join(t.TempDir(), "model.gguf"), Alias: "demo"}
	spec, err := m.backendLaunch(49152, 2048, 4, "0", GGUFInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Binary != bin || spec.ReadyPath != "/v1/models" || spec.APIKey != "" {
		t.Fatalf("unsafe mistral.rs launch: %+v", spec)
	}
	got := strings.Join(spec.Args, " ")
	for _, arg := range []string{"serve", "--host 127.0.0.1", "--port 49152", "--no-ui", "--cpu", "-f " + m.ModelPath} {
		if !strings.Contains(got, arg) {
			t.Fatalf("missing %q: %s", arg, got)
		}
	}
	if strings.Contains(got, "--agent") || strings.Contains(got, "--enable-shell") {
		t.Fatalf("mistral.rs agent execution must never be enabled: %s", got)
	}
	m.RPCServers = []string{"127.0.0.1:2233"}
	if _, err := m.backendLaunch(49152, 2048, 4, "auto", GGUFInfo{}); err == nil {
		t.Fatal("mistral.rs must not accept incompatible GGML RPC Guests")
	}
	m.RPCServers = nil
	m.KVCacheType = "q4_0"
	if _, err := m.backendLaunch(49152, 2048, 4, "auto", GGUFInfo{}); err == nil {
		t.Fatal("llama.cpp-only KV tuning must be rejected, not silently ignored")
	}
}

func TestMistralRSReadinessRequiresLoadedModel(t *testing.T) {
	check := func(status int, raw string) bool {
		t.Helper()
		return backendIsReady(BackendMistralRS, &http.Response{StatusCode: status,
			Body: io.NopCloser(strings.NewReader(raw))})
	}
	if check(200, `{"data":[{"status":"unloaded"}]}`) || check(503, `{"data":[{"status":"loaded"}]}`) ||
		check(200, `{"data":[]}`) || check(200, `{broken}`) {
		t.Fatal("unloaded, invalid, or non-200 runtime cannot become ready")
	}
	if !check(200, `{"data":[{"id":"demo","status":"loaded"}]}`) {
		t.Fatal("loaded model was not detected")
	}
}

func TestMistralModelNormalizationPreservesTools(t *testing.T) {
	m := &Managed{Backend: BackendMistralRS}
	req := protocol.ChatRequest{Model: "original.gguf"}
	m.normalizeModel(&req)
	if req.Model != "default" {
		t.Fatalf("mistral model routing: %q", req.Model)
	}
	body := []byte(`{"model":"original.gguf","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"ping"}}]}`)
	out, err := m.normalizeOpenAI(body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded["model"]) != `"default"` || !bytes.Equal(decoded["messages"], json.RawMessage(`[{"role":"user","content":"hello"}]`)) {
		t.Fatalf("lost model routing or messages: %s", out)
	}
	if !bytes.Equal(decoded["tools"], json.RawMessage(`[{"type":"function","function":{"name":"ping"}}]`)) {
		t.Fatalf("lost tool payload: %s", out)
	}
	defaultBackend := &Managed{}
	unchanged, err := defaultBackend.normalizeOpenAI(body)
	if err != nil || !bytes.Equal(unchanged, body) {
		t.Fatal("llama.cpp request must remain unchanged")
	}
}

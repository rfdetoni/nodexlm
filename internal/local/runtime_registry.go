package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Registered engines use the same OpenAI transport, not a shared tensor ABI.
// A listed engine is an adapter candidate, not proof of installed binaries.
type RuntimeDescriptor struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Mode     string `json:"mode"`
	Protocol string `json:"protocol"`
}

func RuntimeRegistry() []RuntimeDescriptor {
	return []RuntimeDescriptor{
		{"llama.cpp", "llama.cpp", "managed / tensor-rpc", "gguf"},
		{"trellis", "Strix / Agention Trellis", "managed Host / experimental desktop CPU tensor-rpc", "gguf"},
		{"mistralrs", "mistral.rs", "managed host / whole-model worker", "openai"},
		{"ollama", "Ollama", "whole-model worker", "openai"},
		{"vllm", "vLLM", "whole-model worker", "openai"},
		{"sglang", "SGLang", "whole-model worker", "openai"},
		{"mlx-lm", "MLX-LM", "whole-model worker", "openai"},
		{"litertlm", "LiteRT-LM", "whole-model worker", "openai"},
		{"ik_llama.cpp", "ik_llama.cpp", "whole-model worker", "openai"},
		{"llamafile", "llamafile", "whole-model worker", "openai"},
		{"koboldcpp", "KoboldCpp", "whole-model worker", "openai"},
		{"lmstudio", "LM Studio", "whole-model worker", "openai"},
		{"tabbyapi", "TabbyAPI", "whole-model worker", "openai"},
		{"tgi", "Text Generation Inference", "whole-model worker", "openai"},
		{"tensorrt-llm", "TensorRT-LLM", "whole-model worker", "openai"},
		{"openvino", "OpenVINO Model Server", "whole-model worker", "openai"},
		{"openai", "Other local OpenAI-compatible server", "whole-model worker", "openai"},
	}
}

func IsRegisteredRuntime(id string) bool {
	for _, runtime := range RuntimeRegistry() {
		if runtime.ID == id {
			return true
		}
	}
	return false
}

// NewRegisteredRuntime accepts only an operator-configured local origin.
// Model IDs are validated against the actual registry before enrollment.
func NewRegisteredRuntime(ctx context.Context, id, endpoint, model string) (*Engine, error) {
	if !IsRegisteredRuntime(id) {
		return nil, fmt.Errorf("unknown runtime %q", id)
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, errors.New("runtime requires a numeric HTTP loopback origin without credentials or path")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() || u.Port() == "" {
		return nil, errors.New("runtime requires a literal loopback IP and explicit port")
	}
	engine, err := New(endpoint)
	if err != nil {
		return nil, err
	}
	engine.APIKey = os.Getenv("NODEXLM_RUNTIME_API_KEY")
	if err := engine.CheckModel(ctx, model); err != nil {
		engine.HTTP.CloseIdleConnections()
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	return engine, nil
}

// CheckModel also runs periodically on registered workers so stale model
// inventories are not eligible for new jobs. No model download is triggered.
func (e *Engine) CheckModel(ctx context.Context, model string) error {
	if model == "" || len(model) > 256 || strings.ContainsAny(model, "\r\n\x00") {
		return errors.New("invalid model registry ID")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, e.BackendURL+"/v1/models", nil)
	if err != nil {
		return err
	}
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("runtime server unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("runtime model registry returned HTTP %d", resp.StatusCode)
	}
	var registry struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&registry); err != nil {
		return fmt.Errorf("invalid runtime model registry: %w", err)
	}
	for _, entry := range registry.Data {
		if entry.ID == model {
			return nil
		}
	}
	return errors.New("model not found in runtime registry")
}

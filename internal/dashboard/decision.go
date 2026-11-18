package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/local"
	compat "github.com/rfdetoni/nodexlm/internal/openai"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

const ollayaPrefix = "ollaya/"

// Only connect to literal loopback IP addresses: no DNS, redirects, proxy
// environment variables or request-controlled URLs can reach other networks.
var decisionClient = &http.Client{
	Transport: &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("decision backend redirects are forbidden")
	},
}

func decisionBackendURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("NODEXLM_OLLAYA_URL must be a loopback HTTP origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("NODEXLM_OLLAYA_URL must use a numeric loopback IP address")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

// decide preserves native llama.cpp GGUF distribution and optionally bridges
// local Ollaya runtimes (ONNX and additional calibrated decision families).
// Models under ollaya/* never enter the GGML/RPC resident-model manager.
func (m *Manager) decide(ctx context.Context, model, path string, body []byte, receive func(int, http.Header, io.Reader) error) error {
	if !strings.HasPrefix(model, ollayaPrefix) {
		if !m.HasModel(model) {
			return &compat.APIError{Status: 404, Message: "Requested decision model is not loaded", Param: "model", Code: "model_not_found"}
		}
		return m.withGeneration(ctx, protocol.ChatRequest{Model: model}, func(ctx context.Context, engine *local.Managed, _ protocol.ChatRequest) error {
			if local.EffectiveBackend(engine.Backend) != local.BackendLlama {
				return &compat.APIError{Status: 422, Message: "Native decisions require llama.cpp", Param: "model", Code: "unsupported_backend"}
			}
			return engine.OpenAI(ctx, path, body, receive)
		})
	}
	modelName := strings.TrimPrefix(model, ollayaPrefix)
	if modelName == "" || strings.TrimSpace(modelName) != modelName {
		return &compat.APIError{Status: 400, Message: "Invalid Ollaya model name", Param: "model", Code: "invalid_parameter"}
	}
	configured := os.Getenv("NODEXLM_OLLAYA_URL")
	if configured == "" {
		return &compat.APIError{Status: 503, Message: "Ollaya is disabled; configure NODEXLM_OLLAYA_URL", Param: "model", Code: "decision_backend_unavailable"}
	}
	base, err := decisionBackendURL(configured)
	if err != nil {
		return &compat.APIError{Status: 503, Message: err.Error(), Param: "model", Code: "decision_backend_unavailable"}
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil {
		return &compat.APIError{Status: 400, Message: "Invalid decision payload", Code: "invalid_json"}
	}
	payload["model"], _ = json.Marshal(modelName)
	forwarded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// The upstream API key is independent from the NodeXLM admin and inference
	// tokens. Client Authorization is never forwarded to the local runner.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(forwarded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if key := os.Getenv("NODEXLM_OLLAYA_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := decisionClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return receive(response.StatusCode, response.Header, response.Body)
}

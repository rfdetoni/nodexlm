package dashboard

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/external"
	"github.com/rfdetoni/nodexlm/internal/imagegen"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func fallbackConfigured() bool {
	return os.Getenv("NODEXLM_FALLBACK_URL") != "" && os.Getenv("NODEXLM_FALLBACK_MODEL") != "" && os.Getenv("NODEXLM_FALLBACK_API_KEY") != ""
}

// Explicit per-request consent. No implicit redirection after streamed output,
// auth failures, cancellation or a runtime error.
func (d *Dashboard) fallback(w http.ResponseWriter, r *http.Request) {
	if !fallbackConfigured() {
		failure(w, 503, errors.New("external fallback not configured on Host"))
		return
	}
	var req protocol.ChatRequest
	if !decode(w, r, &req) {
		return
	}
	if why := protocol.ValidateChat(req); why != "" {
		failure(w, 400, errors.New(why))
		return
	}
	if req.TargetNode != "" || req.Stream || d.Manager.HasModel(req.Model) {
		failure(w, 409, errors.New("fallback requires unavailable local model, text-only and nonstreaming request"))
		return
	}
	tokens := req.MaxTokens
	if tokens == 0 {
		tokens = 512
	}
	answer, err := external.Chat(r.Context(), os.Getenv("NODEXLM_FALLBACK_URL"), os.Getenv("NODEXLM_FALLBACK_MODEL"), os.Getenv("NODEXLM_FALLBACK_API_KEY"), req.Messages, tokens)
	if err != nil {
		failure(w, 502, err)
		return
	}
	reply(w, 200, map[string]string{"answer": answer, "provider": "external"})
}

// A single configured local diffusion model. Reuse the pool admission lock,
// never evict resident LLMs or accept arbitrary client executable/model paths.
func (d *Dashboard) generateImage(w http.ResponseWriter, r *http.Request) {
	model := os.Getenv("NODEXLM_SD_MODEL")
	if model == "" {
		failure(w, 503, errors.New("local diffusion model not configured"))
		return
	}
	var input struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		Size           string `json:"size"`
		N              int    `json:"n"`
		ResponseFormat string `json:"response_format"`
		Steps          int    `json:"steps"`
		Seed           *int64 `json:"seed"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.Model != "" && input.Model != "stable-diffusion" || input.N != 0 && input.N != 1 ||
		input.ResponseFormat != "" && input.ResponseFormat != "b64_json" {
		failure(w, 400, errors.New("only configured stable-diffusion, one image and b64_json supported"))
		return
	}
	if input.Size == "" {
		input.Size = "512x512"
	}
	x, y, ok := strings.Cut(input.Size, "x")
	width, xerr := strconv.Atoi(x)
	height, yerr := strconv.Atoi(y)
	if !ok || xerr != nil || yerr != nil {
		failure(w, 400, errors.New("size must be WIDTHxHEIGHT"))
		return
	}
	if input.Steps == 0 {
		input.Steps = 20
	}
	seed := int64(-1)
	if input.Seed != nil {
		seed = *input.Seed
	}
	if err := imagegen.Validate(width, height, input.Steps, seed); err != nil {
		failure(w, 400, err)
		return
	}
	m := d.Manager
	if !m.poolMu.TryLock() {
		failure(w, 409, errors.New("model operation in progress"))
		return
	}
	defer m.poolMu.Unlock()
	m.mu.Lock()
	busy := m.closed || m.loading || m.unloading || m.engine != nil || len(m.residents) != 0
	m.mu.Unlock()
	if busy {
		failure(w, 409, errors.New("unload all language models before generating an image"))
		return
	}
	stage, err := os.MkdirTemp("", "nodexlm-diffusion-")
	if err != nil {
		failure(w, 500, errors.New("cannot stage image"))
		return
	}
	defer os.RemoveAll(stage)
	binary := os.Getenv("NODEXLM_SD_BIN")
	if binary == "" {
		binary = "sd-cli"
	}
	output := filepath.Join(stage, "output.png")
	if err := imagegen.Run(r.Context(), binary, model, input.Prompt, output, width, height, input.Steps, seed); err != nil {
		failure(w, 502, errors.New("diffusion generation failed; check configured local runtime"))
		return
	}
	png, err := os.ReadFile(output)
	if err != nil {
		failure(w, 500, errors.New("image unreadable"))
		return
	}
	reply(w, 200, map[string]any{"created": time.Now().Unix(), "data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(png)}}})
}

package openai

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Forward func(context.Context, string, string, []byte, func(int, http.Header, io.Reader) error) error
type Handler struct {
	Forward Forward
	Target  http.HandlerFunc
	Models  func() []string
	Observe func(string, []byte, time.Duration)
	store   responseStore
}

func IsPath(path string) bool {
	for _, p := range []string{"/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings", "/v1/models"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	w.Header().Set("X-Request-ID", "req_"+hex.EncodeToString(id))
	ownerHash := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	owner := hex.EncodeToString(ownerHash[:])
	path := r.URL.Path
	if path == "/v1/models" || strings.HasPrefix(path, "/v1/models/") {
		h.models(w, r)
		return
	}
	if strings.HasPrefix(path, "/v1/responses/") && path != "/v1/responses/input_tokens" {
		h.savedResponse(w, r, owner)
		return
	}
	allowed := path == "/v1/chat/completions" || path == "/v1/completions" || path == "/v1/responses" || path == "/v1/embeddings" || path == "/v1/responses/input_tokens" || path == "/v1/chat/completions/input_tokens"
	if !allowed {
		Error(w, 404, "Unknown inference endpoint", "", "not_found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		Error(w, 405, "Use POST for this endpoint", "", "method_not_allowed")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		Error(w, 415, "Content-Type must be application/json", "", "invalid_content_type")
		return
	}
	var input map[string]json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	if err := decoder.Decode(&input); err != nil || input == nil {
		Error(w, 400, "Expected a JSON object within the request size limit", "", "invalid_json")
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		Error(w, 400, "Unexpected trailing JSON data", "", "invalid_json")
		return
	}
	model, err := stringField(input, "model")
	if err != nil || model == "" {
		Error(w, 400, "model is required and must be a string", "model", "invalid_parameter")
		return
	}
	if target, ok := input["target_node"]; ok && string(target) != "null" && string(target) != `""` {
		if path != "/v1/chat/completions" || h.Target == nil {
			Error(w, 400, "target_node is available only for standalone text chat", "target_node", "unsupported_parameter")
			return
		}
		// Preserve the existing standalone-worker extension; agent calls use the
		// Host model, whose native execution can distribute layers through RPC.
		allowed := map[string]bool{"model": true, "messages": true, "max_tokens": true, "temperature": true, "top_p": true, "stop": true, "stream": true, "stream_options": true, "cache_prompt": true, "priority": true, "reasoning_effort": true, "reasoning_budget_tokens": true, "target_node": true}
		for key := range input {
			if !allowed[key] {
				Error(w, 400, "Advanced agent inference uses the Host model and its RPC Guests", key, "unsupported_parameter")
				return
			}
		}
		body, _ := json.Marshal(input)
		r.Body = io.NopCloser(bytes.NewReader(body))
		h.Target(w, r)
		return
	}
	delete(input, "target_node")
	legacy := path == "/v1/chat/completions" && usesLegacy(input)
	stream := false
	if raw := input["stream"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &stream) != nil {
			Error(w, 400, "stream must be a boolean", "stream", "invalid_parameter")
			return
		}
	}
	if err := normalize(input, path); err != nil {
		var api *APIError
		if errors.As(err, &api) {
			Error(w, api.Status, api.Message, api.Param, api.Code)
		} else {
			Error(w, 400, err.Error(), "", "invalid_parameter")
		}
		return
	}
	previousResponse, _ := stringField(input, "previous_response_id")
	var history []json.RawMessage
	keep := false
	if path == "/v1/responses" {
		history, keep, err = h.prepareResponse(input, owner)
		if err != nil {
			var api *APIError
			if errors.As(err, &api) {
				Error(w, api.Status, api.Message, api.Param, api.Code)
			} else {
				Error(w, 400, err.Error(), "input", "invalid_parameter")
			}
			return
		}
	}
	raw, _ := json.Marshal(input)
	if len(raw) > 8<<20 {
		Error(w, 400, "Conversation exceeds the request size limit", "input", "context_length_exceeded")
		return
	}
	start := time.Now()
	started := false
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	err = h.Forward(ctx, model, path, raw, func(status int, headers http.Header, reader io.Reader) error {
		if status < 200 || status >= 300 {
			body, e := io.ReadAll(io.LimitReader(reader, 1<<20))
			if e != nil {
				return e
			}
			started = true
			forwardError(w, status, body)
			return errResponseWritten
		}
		if stream && strings.HasPrefix(headers.Get("Content-Type"), "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Accel-Buffering", "no")
			started = true
			return relayStream(ctx, w, reader, path, legacy, previousResponse, func(body []byte) {
				if keep {
					h.remember(body, history, owner)
				}
			}, func(payload []byte) {
				if h.Observe != nil {
					h.Observe(model, payload, time.Since(start))
				}
			})
		}
		if stream {
			return fmt.Errorf("runtime did not return an event stream")
		}
		body, e := io.ReadAll(io.LimitReader(reader, 16<<20+1))
		if e != nil {
			return e
		}
		if len(body) > 16<<20 || !json.Valid(body) {
			return errors.New("invalid or oversized runtime response")
		}
		if path == "/v1/responses" {
			body, e = normalizeResponse(body, previousResponse)
			if e != nil {
				return e
			}
		}
		if legacy {
			body = legacyChatResponse(body, false)
		}
		if path == "/v1/responses" && keep && !h.remember(body, history, owner) {
			return errors.New("runtime response could not be retained")
		}
		if h.Observe != nil {
			h.Observe(model, body, time.Since(start))
		}
		started = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, e = w.Write(body)
		return e
	})
	if errors.Is(err, errResponseWritten) {
		return
	}
	if err != nil && r.Context().Err() == nil {
		if !started {
			var api *APIError
			if errors.As(err, &api) {
				Error(w, api.Status, api.Message, api.Param, api.Code)
			} else {
				status := 502
				if errors.Is(err, context.DeadlineExceeded) {
					status = 504
				}
				Error(w, status, err.Error(), "", "inference_error")
			}
		} else {
			_ = streamError(w, path, err)
		}
	}
}
func stringField(m map[string]json.RawMessage, key string) (string, error) {
	var s string
	if raw := m[key]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
	}
	return s, nil
}
func (h *Handler) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		Error(w, 405, "Use GET for model discovery", "", "method_not_allowed")
		return
	}
	items := []any{}
	for _, name := range h.Models() {
		item := map[string]any{"id": name, "object": "model", "created": 0, "owned_by": "nodexlm"}
		if r.URL.Path == "/v1/models/"+name {
			JSON(w, 200, item)
			return
		}
		items = append(items, item)
	}
	if r.URL.Path != "/v1/models" {
		Error(w, 404, "Model is not loaded", "model", "model_not_found")
		return
	}
	JSON(w, 200, map[string]any{"object": "list", "data": items})
}
func forwardError(w http.ResponseWriter, status int, body []byte) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) == nil {
		var nested map[string]any
		if json.Unmarshal(obj["error"], &nested) == nil && nested["message"] != nil {
			if nested["type"] == nil {
				nested["type"] = "invalid_request_error"
			}
			if _, ok := nested["param"]; !ok {
				nested["param"] = nil
			}
			if _, ok := nested["code"]; !ok {
				nested["code"] = nil
			}
			JSON(w, status, map[string]any{"error": nested})
			return
		}
	}
	message := http.StatusText(status)
	var val map[string]any
	if json.Unmarshal(body, &val) == nil {
		if s, ok := val["error"].(string); ok {
			message = s
		}
	}
	Error(w, status, message, "", "runtime_error")
}

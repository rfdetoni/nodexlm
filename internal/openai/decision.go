package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// decisions preserves System One's own request and response format. It is not
// a text-completions adapter: probabilities must come from the decision runtime.
func (h *Handler) decisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		Error(w, http.StatusMethodNotAllowed, "Use POST for this endpoint", "", "method_not_allowed")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		Error(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json", "", "invalid_content_type")
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	var input map[string]json.RawMessage
	if decoder.Decode(&input) != nil || input == nil {
		Error(w, 400, "Expected a JSON object within the request size limit", "", "invalid_json")
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		Error(w, 400, "Unexpected trailing JSON data", "", "invalid_json")
		return
	}
	model, err := stringField(input, "model")
	if err != nil || strings.TrimSpace(model) == "" {
		Error(w, 400, "model must be a nonempty string", "model", "invalid_parameter")
		return
	}
	if err := validateDecision(input); err != nil {
		Error(w, 400, err.Error(), "questions", "invalid_parameter")
		return
	}
	if h.Decision == nil {
		Error(w, 501, "Decision inference is not configured", "", "unsupported_operation")
		return
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 8<<20 {
		Error(w, 413, "Decision request is too large", "", "request_too_large")
		return
	}
	timeout := 4 * time.Minute
	if h.Timeout != nil && !strings.HasPrefix(model, "ollaya/") {
		timeout = h.Timeout(model)
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	written := false
	err = h.Decision(ctx, model, "/v1/systemone", raw, func(status int, headers http.Header, source io.Reader) error {
		limit := int64(16 << 20)
		if status < 200 || status >= 300 {
			limit = 1 << 20
		}
		payload, readErr := io.ReadAll(io.LimitReader(source, limit+1))
		if readErr != nil {
			return readErr
		}
		if int64(len(payload)) > limit || !json.Valid(payload) {
			return errors.New("decision runtime returned invalid or oversized JSON")
		}
		if id := headers.Get("x-typesafe-request-id"); id != "" {
			w.Header().Set("x-typesafe-request-id", id)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		written = true
		_, writeErr := w.Write(payload)
		return writeErr
	})
	if err != nil && !written && r.Context().Err() == nil {
		var api *APIError
		if errors.As(err, &api) {
			Error(w, api.Status, api.Message, api.Param, api.Code)
		} else if errors.Is(err, context.DeadlineExceeded) {
			Error(w, 504, "Decision inference timed out", "", "decision_timeout")
		} else {
			Error(w, 502, "Decision inference backend failed", "", "decision_backend_error")
		}
	}
}

// Validate the interoperable typed-question envelope without reinterpreting
// states, instructions, metadata, or calibrated probability output.
func validateDecision(input map[string]json.RawMessage) error {
	state, ok := input["state"]
	if !ok || len(state) == 0 {
		return errors.New("state is required")
	}
	if state[0] != '"' && state[0] != '{' && state[0] != '[' && string(state) != "null" {
		return errors.New("state must be a string, object, array or null")
	}
	var questions map[string]json.RawMessage
	if json.Unmarshal(input["questions"], &questions) != nil || len(questions) < 1 || len(questions) > 64 {
		return errors.New("questions must be an object containing 1 to 64 questions")
	}
	for id, raw := range questions {
		if strings.TrimSpace(id) == "" {
			return errors.New("question IDs cannot be empty")
		}
		var q struct {
			Type         string          `json:"type"`
			Instructions json.RawMessage `json:"instructions"`
			Criteria     json.RawMessage `json:"criteria"`
		}
		if json.Unmarshal(raw, &q) != nil || len(q.Instructions) == 0 || string(q.Instructions) == "null" {
			return errors.New("each question requires a type and instructions")
		}
		if v := q.Instructions[0]; v != '"' && v != '{' && v != '[' {
			return errors.New("instructions must be a string, object or array")
		}
		switch q.Type {
		case "noul":
			if len(q.Criteria) > 0 && string(q.Criteria) != "null" {
				var c map[string]json.RawMessage
				if json.Unmarshal(q.Criteria, &c) != nil || len(c) > 2 {
					return errors.New("noul criteria must be an object")
				}
				for k := range c {
					if k != "true" && k != "false" {
						return errors.New("noul criteria only accept true and false")
					}
				}
			}
		case "choice":
			var c map[string]json.RawMessage
			if json.Unmarshal(q.Criteria, &c) == nil && len(c) >= 2 && len(c) <= 255 {
				break
			}
			var a []string
			if json.Unmarshal(q.Criteria, &a) != nil || len(a) < 2 || len(a) > 255 {
				return errors.New("choice requires 2 to 255 options")
			}
		case "score":
			var c []json.RawMessage
			if json.Unmarshal(q.Criteria, &c) != nil || len(c) < 2 || len(c) > 10 {
				return errors.New("score requires 2 to 10 ordered criteria")
			}
		default:
			return errors.New("question type must be choice, score or noul")
		}
	}
	return nil
}

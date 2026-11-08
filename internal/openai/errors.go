// Package openai exposes the local runtime's OpenAI inference contracts without
// reducing messages, tool calls or named streaming events to plain text.
package openai

import (
	"encoding/json"
	"errors"
	"net/http"
)

var errResponseWritten = errors.New("runtime error response already written")

type APIError struct {
	Status               int
	Message, Param, Code string
}

func (e *APIError) Error() string { return e.Message }
func Error(w http.ResponseWriter, status int, message, param, code string) {
	kind := "invalid_request_error"
	if status == 401 {
		kind = "authentication_error"
	}
	if status == 429 {
		kind = "rate_limit_error"
	}
	if status >= 500 {
		kind = "server_error"
	}
	var field any
	if param != "" {
		field = param
	}
	var errorCode any
	if code != "" {
		errorCode = code
	}
	JSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": kind, "param": field, "code": errorCode}})
}
func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

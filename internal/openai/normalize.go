package openai

import (
	"encoding/json"
	"fmt"
	"math"
)

func invalid(param, message string) error {
	return &APIError{Status: 400, Message: message, Param: param, Code: "invalid_parameter"}
}
func normalize(in map[string]json.RawMessage, path string) error {
	if extra := in["extra_params"]; len(extra) > 0 && string(extra) != "null" {
		var params map[string]json.RawMessage
		if json.Unmarshal(extra, &params) != nil {
			return invalid("extra_params", "extra_params must be an object")
		}
		for _, key := range []string{"model", "messages", "input", "tools", "tool_choice", "stream", "modalities", "audio", "response_format", "reasoning_effort", "reasoning_budget_tokens"} {
			if _, ok := params[key]; ok {
				return invalid("extra_params."+key, "extra_params cannot replace OpenAI request fields")
			}
		}
	}
	if value := in["conversation"]; len(value) > 0 && string(value) != "null" {
		return invalid("conversation", "Use previous_response_id or resend input history; hosted Conversations are not enabled")
	}

	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "n", "top_logprobs"} {
		if raw := in[key]; len(raw) > 0 && string(raw) != "null" {
			var n int
			if json.Unmarshal(raw, &n) != nil || n < 0 {
				return invalid(key, key+" must be a nonnegative integer")
			}
		}
	}
	for key, limits := range map[string][2]float64{"temperature": {0, 2}, "top_p": {0, 1}, "frequency_penalty": {-2, 2}, "presence_penalty": {-2, 2}} {
		if raw := in[key]; len(raw) > 0 && string(raw) != "null" {
			var n float64
			if json.Unmarshal(raw, &n) != nil || math.IsNaN(n) || n < limits[0] || n > limits[1] {
				return invalid(key, fmt.Sprintf("%s must be between %g and %g", key, limits[0], limits[1]))
			}
		}
	}
	if raw := in["max_completion_tokens"]; len(raw) > 0 && string(raw) != "null" {
		if legacy := in["max_tokens"]; len(legacy) > 0 && string(legacy) != "null" {
			return invalid("max_completion_tokens", "Use max_completion_tokens or max_tokens, not both")
		}
		in["max_tokens"] = raw
		delete(in, "max_completion_tokens")
	}
	for _, key := range []string{"background"} {
		if string(in[key]) == "true" {
			return invalid(key, "Background execution is not supported; use streaming or synchronous inference")
		}
	}
	if raw := in["modalities"]; len(raw) > 0 && string(raw) != "null" {
		var values []string
		if json.Unmarshal(raw, &values) != nil {
			return invalid("modalities", "modalities must be an array")
		}
		for _, v := range values {
			if v != "text" {
				return invalid("modalities", "The managed runtime currently exposes text output only")
			}
		}
	}
	if len(in["audio"]) > 0 && string(in["audio"]) != "null" {
		return invalid("audio", "Audio generation requires an audio-capable runtime")
	}
	if path == "/v1/chat/completions" || path == "/v1/chat/completions/input_tokens" {
		if err := normalizeReasoningControls(in); err != nil {
			return err
		}
		var messages []map[string]json.RawMessage
		if json.Unmarshal(in["messages"], &messages) != nil || len(messages) == 0 {
			return invalid("messages", "messages must be a nonempty array")
		}
		for _, m := range messages {
			role, e := stringField(m, "role")
			if e != nil {
				return invalid("messages", "Invalid role")
			}
			switch role {
			case "user", "assistant", "system", "developer", "tool", "function":
			default:
				return invalid("messages", "Invalid message role")
			}
			if e := textContent(m["content"]); e != nil {
				return e
			}
			if role == "tool" {
				id, _ := stringField(m, "tool_call_id")
				if id == "" {
					return invalid("messages.tool_call_id", "Tool messages require tool_call_id")
				}
			}
		}
		if err := normalizeLegacy(in, messages); err != nil {
			return err
		}
	}
	if raw := in["tools"]; len(raw) > 0 && string(raw) != "null" {
		var tools []map[string]json.RawMessage
		if json.Unmarshal(raw, &tools) != nil {
			return invalid("tools", "tools must be an array")
		}
		for _, tool := range tools {
			kind, _ := stringField(tool, "type")
			if kind != "function" {
				return invalid("tools", "Use client-executed function tools; hosted OpenAI tools are not provided by a local model")
			}
		}
	}
	return nil
}
func textContent(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return nil
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return invalid("content", "content must be a string, null or text content parts")
	}
	for _, part := range parts {
		kind, _ := stringField(part, "type")
		switch kind {
		case "text", "input_text", "output_text", "refusal":
		default:
			return invalid("content", "Image, audio, file and video content require a multimodal model/runtime")
		}
	}
	return nil
}

// Reasoning controls are forwarded intact to llama.cpp after validating
// types and contradictory combinations. Models may ignore effort levels
// absent matching Jinja chat-template capabilities.
func normalizeReasoningControls(in map[string]json.RawMessage) error {
	var effort string
	if raw := in["reasoning_effort"]; len(raw) != 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &effort) != nil {
			return invalid("reasoning_effort", "reasoning_effort must be a string")
		}
		switch effort {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		default:
			return invalid("reasoning_effort", "Unsupported reasoning_effort for llama.cpp")
		}
	}
	budget := 0
	if raw := in["reasoning_budget_tokens"]; len(raw) != 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &budget) != nil || budget < 0 || budget > 262144 {
			return invalid("reasoning_budget_tokens", "reasoning_budget_tokens must be an integer between 0 and 262144")
		}
	}
	if effort == "none" && budget > 0 {
		return invalid("reasoning_budget_tokens", "reasoning_budget_tokens cannot be positive when reasoning_effort is none")
	}
	if budget > 0 {
		if raw := in["max_tokens"]; len(raw) != 0 && string(raw) != "null" {
			var maxTokens int
			if json.Unmarshal(raw, &maxTokens) == nil && maxTokens > 0 && budget > maxTokens {
				return invalid("reasoning_budget_tokens", "reasoning_budget_tokens cannot exceed max_tokens")
			}
		}
	}
	return nil
}

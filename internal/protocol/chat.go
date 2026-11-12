package protocol

import (
	"encoding/json"
	"errors"
	"math"
)

// Stop accepts the OpenAI string or array representation without changing it.
type Stop json.RawMessage

func (s *Stop) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		*s = nil
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		if len(one) > 1024 {
			return errors.New("stop too long")
		}
		*s = append((*s)[:0], raw...)
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil || len(many) > 4 {
		return errors.New("stop must be a string or up to four strings")
	}
	for _, v := range many {
		if len(v) > 1024 {
			return errors.New("stop too long")
		}
	}
	*s = append((*s)[:0], raw...)
	return nil
}
func (s Stop) MarshalJSON() ([]byte, error) {
	if len(s) == 0 {
		return []byte("null"), nil
	}
	return s, nil
}

func ValidateChat(req ChatRequest) string {
	if req.Model == "" || len(req.Model) > 256 {
		return "model is required"
	}
	if len(req.Messages) < 1 || len(req.Messages) > 64 {
		return "1-64 messages required"
	}
	if req.MaxTokens < 0 || req.MaxTokens > 4096 {
		return "max_tokens exceeds limit"
	}
	switch req.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		return "reasoning_effort must be none, minimal, low, medium, high, xhigh or max"
	}
	if req.ReasoningBudgetTokens < 0 || req.ReasoningBudgetTokens > 4096 {
		return "reasoning_budget_tokens must be between 0 and 4096"
	}
	if req.ReasoningEffort == "none" && req.ReasoningBudgetTokens > 0 {
		return "reasoning_budget_tokens cannot be set when reasoning_effort is none"
	}
	if req.ReasoningBudgetTokens > 0 && req.MaxTokens > 0 && req.ReasoningBudgetTokens > req.MaxTokens {
		return "reasoning_budget_tokens cannot exceed max_tokens"
	}
	if math.IsNaN(req.Temperature) || math.IsInf(req.Temperature, 0) || req.Temperature < 0 || req.Temperature > 2 {
		return "temperature must be between 0 and 2"
	}
	if req.TopP != nil && (math.IsNaN(*req.TopP) || math.IsInf(*req.TopP, 0) || *req.TopP < 0 || *req.TopP > 1) {
		return "top_p must be between 0 and 1"
	}
	if req.Priority < -10 || req.Priority > 10 {
		return "priority must be between -10 and 10"
	}
	if len(req.Stop) > 0 {
		var stop Stop
		if err := json.Unmarshal(req.Stop, &stop); err != nil {
			return err.Error()
		}
	}
	for _, m := range req.Messages {
		if m.Role != "user" && m.Role != "assistant" && m.Role != "system" {
			return "invalid message role"
		}
		if len(m.Content) > 65536 {
			return "message too large"
		}
	}
	return ""
}

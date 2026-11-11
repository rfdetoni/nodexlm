package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatReasoningControls(t *testing.T) {
	req := ChatRequest{
		Model:                 "qwen.gguf",
		Messages:              []Message{{Role: "user", Content: "why"}},
		MaxTokens:             512,
		ReasoningEffort:       "high",
		ReasoningBudgetTokens: 256,
	}
	if why := ValidateChat(req); why != "" {
		t.Fatal(why)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"\"reasoning_effort\":\"high\"", "\"reasoning_budget_tokens\":256"} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("lost llama.cpp reasoning control %q in %s", key, raw)
		}
	}
	for _, effort := range []string{"", "none", "low", "medium", "high", "xhigh"} {
		req.ReasoningEffort = effort
		req.ReasoningBudgetTokens = 0
		if why := ValidateChat(req); why != "" {
			t.Fatalf("%q: %s", effort, why)
		}
	}
	for _, bad := range []struct {
		effort string
		budget int
	}{
		{"extreme", 0}, {"none", 64}, {"low", -1}, {"medium", 513}, {"high", 4097},
	} {
		req.ReasoningEffort = bad.effort
		req.ReasoningBudgetTokens = bad.budget
		if why := ValidateChat(req); why == "" {
			t.Fatalf("accepted invalid reasoning controls %+v", bad)
		}
	}
}

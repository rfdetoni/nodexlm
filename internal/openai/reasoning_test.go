package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestChatReasoningEffortAndBudgetReachNativeRuntime(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		t.Run(effort, func(t *testing.T) {
			var forwarded map[string]json.RawMessage
			h := &Handler{Forward: func(_ context.Context, _, path string, body []byte, receive func(int, http.Header, io.Reader) error) error {
				if path != "/v1/chat/completions" {
					t.Fatalf("wrong path: %q", path)
				}
				if err := json.Unmarshal(body, &forwarded); err != nil {
					t.Fatal(err)
				}
				return receive(http.StatusOK, nil, strings.NewReader(`{"id":"test","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			}}
			budget := ""
			if effort != "none" {
				budget = `,"reasoning_budget_tokens":128`
			}
			requestBody := `{"model":"qwen.gguf","messages":[{"role":"user","content":"test"}],"max_completion_tokens":256,"reasoning_effort":"` + effort + `"` + budget + `}`
			out := request(h, "POST", "/v1/chat/completions", "key", requestBody)
			if out.Code != http.StatusOK {
				t.Fatalf("%d %s", out.Code, out.Body)
			}
			var got string
			if err := json.Unmarshal(forwarded["reasoning_effort"], &got); err != nil || got != effort {
				t.Fatalf("reasoning effort not forwarded: %s", forwarded["reasoning_effort"])
			}
			if effort != "none" && string(forwarded["reasoning_budget_tokens"]) != "128" {
				t.Fatalf("reasoning budget lost: %s", forwarded["reasoning_budget_tokens"])
			}
			if string(forwarded["max_tokens"]) != "256" {
				t.Fatalf("max_completion_tokens should normalize to max_tokens: %s", forwarded["max_tokens"])
			}
		})
	}
}

func TestChatReasoningInvalidParametersRejectedBeforeForward(t *testing.T) {
	h := &Handler{Forward: func(context.Context, string, string, []byte, func(int, http.Header, io.Reader) error) error {
		t.Fatal("invalid reasoning controls reached native runtime")
		return nil
	}}
	for _, suffix := range []string{
		`,"reasoning_effort":"extreme"`,
		`,"reasoning_effort":123`,
		`,"reasoning_budget_tokens":"100"`,
		`,"reasoning_budget_tokens":-2`,
		`,"reasoning_budget_tokens":262145`,
		`,"reasoning_effort":"none","reasoning_budget_tokens":4`,
		`,"max_tokens":12,"reasoning_budget_tokens":13`,
		`,"max_completion_tokens":12,"reasoning_budget_tokens":13`,
	} {
		body := `{"model":"qwen.gguf","messages":[{"role":"user","content":"test"}]` + suffix + `}`
		out := request(h, "POST", "/v1/chat/completions", "key", body)
		if out.Code != http.StatusBadRequest || !strings.Contains(out.Body.String(), "invalid_request_error") {
			t.Fatalf("invalid %s returned HTTP %d: %s", suffix, out.Code, out.Body)
		}
	}
}

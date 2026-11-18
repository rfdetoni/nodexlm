package catalog

import "testing"

func TestIsDecisionModelDiscoveryHints(t *testing.T) {
	for _, test := range []struct {
		repository, filename string
		want                 bool
	}{
		{"vendor/Winnow-E4B", "winnow-Q8_0.gguf", true},
		{"vendor/pplx-decider-flash", "model.gguf", true},
		{"vendor/JevK5", "model.gguf", true},
		{"vendor/strands-decider", "Q4_K_M.gguf", true},
		{"vendor/systemone-model", "model.gguf", true},
		{"vendor/Qwen3-Instruct", "Q4_K_M.gguf", false},
		{"vendor/gemini-like", "decisionless-chat.gguf", false},
		{"vendor/undeclared", "abcd3.gguf", false},
	} {
		if got := IsDecisionModel(test.repository, test.filename); got != test.want {
			t.Errorf("%s/%s: got %t, want %t", test.repository, test.filename, got, test.want)
		}
	}
}

package dashboard

import (
	"testing"
	"time"
)

func TestInferenceResourceValidation(t *testing.T) {
	for _, s := range []Settings{
		{ThreadsBatch: -1}, {PromptCacheMiB: 8193},
		{LoadMode: "invalid"}, {LazyMode: "invalid"}, {NUMA: "invalid"},
		{KVCacheType: "q4_0", FlashAttention: "off"},
		{Distributed: true, LoadMode: "dio"},
	} {
		if validateInferenceResources(s) == nil {
			t.Fatalf("accepted invalid resources: %+v", s)
		}
	}
	if err := validateInferenceResources(Settings{ThreadsBatch: 4, LoadMode: "mmap", LazyMode: "auto", KVCacheType: "q8_0", FlashAttention: "auto"}); err != nil {
		t.Fatal(err)
	}
}
func TestNativePrefillAndDraftMetrics(t *testing.T) {
	raw := []byte(`{"usage":{"completion_tokens":16},"timings":{"prompt_n":100,"prompt_ms":200,"predicted_ms":400,"draft_n":12,"draft_n_accepted":9}}`)
	s, ok := parseInferenceSample("test", raw, time.Second)
	if !ok || s.PrefillTokensPerSecond != 500 || s.DraftAcceptance == nil || *s.DraftAcceptance != .75 {
		t.Fatalf("unexpected timing sample: %+v", s)
	}
}

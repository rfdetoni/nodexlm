package local

import (
	"strings"
	"testing"
)

func TestNativeTuningFlagsOptIn(t *testing.T) {
	basic := &Managed{ModelPath: "example.gguf", Alias: "example"}
	baseline := strings.Join(basic.args(8787, 2048, 4, "auto", 24), " ")
	for _, option := range []string{"--flash-attn", "--cache-type-k", "--batch-size", "--ubatch-size", "--spec-type"} {
		if strings.Contains(baseline, option) {
			t.Fatalf("unexpected default native option %s", option)
		}
	}
	tuned := &Managed{ModelPath: "example.gguf", Alias: "example", FlashAttention: "on", KVCacheType: "q8_0", BatchSize: 1024, UBatchSize: 256, SpeculativeMode: "ngram"}
	got := strings.Join(tuned.args(8787, 2048, 4, "auto", 24), " ")
	for _, option := range []string{"--flash-attn on", "--cache-type-k q8_0", "--cache-type-v q8_0", "--batch-size 1024", "--ubatch-size 256", "--spec-type ngram-simple"} {
		if !strings.Contains(got, option) {
			t.Fatalf("missing native option %s: %s", option, got)
		}
	}

	modelDraft := &Managed{ModelPath: "main.gguf", Alias: "main", SpeculativeMode: "draft", DraftModelPath: "draft.gguf"}
	flags := strings.Join(modelDraft.args(8787, 2048, 4, "auto", 24), " ")
	for _, option := range []string{"--spec-type draft-simple", "--spec-draft-model draft.gguf", "--spec-draft-ngl 0"} {
		if !strings.Contains(flags, option) {
			t.Fatalf("missing draft option %s: %s", option, flags)
		}
	}
}

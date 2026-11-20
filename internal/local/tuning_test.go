package local

import (
	"strings"
	"testing"
)

func TestNativeTuningFlagsOptIn(t *testing.T) {
	basic := &Managed{ModelPath: "example.gguf", Alias: "example"}
	baseline := strings.Join(basic.args(8787, 2048, 4, "auto", 24), " ")
	for _, option := range []string{"--flash-attn", "--cache-type-k", "--batch-size", "--ubatch-size", "--spec-type", "--moe-cache-cpu-share", "--cache-reuse", "--spec-draft-n-max"} {
		if strings.Contains(baseline, option) {
			t.Fatalf("unexpected default native option %s", option)
		}
	}
	tuned := &Managed{ModelPath: "example.gguf", Alias: "example", FlashAttention: "on", KVCacheType: "q8_0", BatchSize: 1024, UBatchSize: 256, SpeculativeMode: "ngram", CacheReuseTokens: 64}
	got := strings.Join(tuned.args(8787, 2048, 4, "auto", 24), " ")
	for _, option := range []string{"--flash-attn on", "--cache-type-k q8_0", "--cache-type-v q8_0", "--batch-size 1024", "--ubatch-size 256", "--spec-type ngram-simple", "--cache-reuse 64"} {
		if !strings.Contains(got, option) {
			t.Fatalf("missing native option %s: %s", option, got)
		}
	}

	modelDraft := &Managed{ModelPath: "main.gguf", Alias: "main", SpeculativeMode: "draft", DraftModelPath: "draft.gguf", SpeculativeTokens: 6}
	flags := strings.Join(modelDraft.args(8787, 2048, 4, "auto", 24), " ")
	for _, option := range []string{"--spec-type draft-simple", "--spec-draft-model draft.gguf", "--spec-draft-ngl 0", "--spec-draft-n-max 6"} {
		if !strings.Contains(flags, option) {
			t.Fatalf("missing draft option %s: %s", option, flags)
		}
	}
}

func TestMoECacheCPUShareFlagsAndValidation(t *testing.T) {
	meta := GGUFInfo{IsMoE: true}
	tuned := &Managed{MoECPU: true, MoECacheMiB: 256, MoECacheCPUShare: 0.25}
	if err := tuned.validateMoE(meta); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(tuned.args(8787, 2048, 4, "auto", 24), " ")
	for _, flag := range []string{"--cpu-moe", "--moe-cache-mib 256", "--moe-cache-cpu-share 0.25"} {
		if !strings.Contains(got, flag) {
			t.Fatalf("missing %q in %s", flag, got)
		}
	}
	for _, invalid := range []*Managed{
		{MoECPU: true, MoECacheCPUShare: 0.25},
		{MoECPU: true, MoECacheMiB: 1, MoECacheCPUShare: 1.1},
		{MoECPU: true, MoECacheMiB: 1, MoECacheCPUShare: -0.1},
		{MoECPU: true, MoECacheMiB: 1, MoECacheCPUShare: 0.5},
	} {
		// Non-MoE GGUF must reject every configured expert placement option.
		if err := invalid.validateMoE(GGUFInfo{}); err == nil {
			t.Fatalf("accepted MoE tuning on dense model: %+v", invalid)
		}
	}
	for _, invalid := range []*Managed{
		{MoECPU: true, MoECacheCPUShare: 0.25},
		{MoECPU: true, MoECacheMiB: 1, MoECacheCPUShare: 1.1},
		{MoECPU: true, MoECacheMiB: 1, MoECacheCPUShare: -0.1},
	} {
		if err := invalid.validateMoE(meta); err == nil {
			t.Fatalf("accepted invalid MoE cache: %+v", invalid)
		}
	}
}

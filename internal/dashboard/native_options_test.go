package dashboard

import "testing"

func TestManagedFromSettingsCopiesNativeOptions(t *testing.T) {
	s := Settings{
		Backend: "llama.cpp", Threads: 6, ContextTokens: 8192, GPULayers: "all",
		MoECPU: true, MoECPULayers: 0, MoECacheMiB: 64,
		FlashAttention: "on", KVCacheType: "q8_0",
		BatchSize: 1024, UBatchSize: 128, SpeculativeMode: "draft",
	}
	got := managedFromSettings(s, "/bin/llama-server", "/models/model.gguf", "model", "/models/draft.gguf")
	if got.Backend != s.Backend || got.Binary != "/bin/llama-server" ||
		got.ModelPath != "/models/model.gguf" || got.Alias != "model" ||
		got.Threads != s.Threads || got.ContextTokens != s.ContextTokens ||
		got.GPULayers != s.GPULayers || got.MoECPU != s.MoECPU ||
		got.MoECPULayers != s.MoECPULayers || got.MoECacheMiB != s.MoECacheMiB ||
		got.FlashAttention != s.FlashAttention || got.KVCacheType != s.KVCacheType ||
		got.BatchSize != s.BatchSize || got.UBatchSize != s.UBatchSize ||
		got.SpeculativeMode != s.SpeculativeMode ||
		got.DraftModelPath != "/models/draft.gguf" || !got.KeepLoaded {
		t.Fatalf("native runtime options were not copied: %+v", got)
	}
}

package dashboard

import "testing"

func TestNativeTuningSettingsValidation(t *testing.T) {
	m := &Manager{}
	base := Settings{Backend: "llama.cpp", Threads: 4, ContextTokens: 2048, GPULayers: "auto"}
	valid := base
	valid.MoECPU, valid.MoECacheMiB, valid.MoECacheCPUShare = true, 256, 0.25
	valid.SpeculativeMode, valid.DraftModel, valid.SpeculativeTokens = "draft", "draft.gguf", 6
	valid.CacheReuseTokens = 128
	if err := m.validate(valid); err != nil {
		t.Fatalf("rejected valid tuning: %v", err)
	}
	ngram := base
	ngram.SpeculativeMode, ngram.SpeculativeTokens = "ngram", 16
	if err := m.validate(ngram); err != nil {
		t.Fatalf("rejected valid ngram tuning: %v", err)
	}
	tests := []struct {
		name string
		edit func(*Settings)
	}{
		{"missing MoE cache", func(s *Settings) { s.MoECacheCPUShare = 0.25 }},
		{"MoE CPU miss share over 100 percent", func(s *Settings) { s.MoECacheCPUShare = 1.5 }},
		{"MoE CPU miss share below zero", func(s *Settings) { s.MoECacheCPUShare = -0.1 }},
		{"KV reuse oversized", func(s *Settings) { s.CacheReuseTokens = 4097 }},
		{"KV reuse negative", func(s *Settings) { s.CacheReuseTokens = -1 }},
		{"speculation without draft", func(s *Settings) { s.SpeculativeTokens = 3 }},
		{"speculation oversized", func(s *Settings) { s.SpeculativeMode = "draft"; s.DraftModel = "draft.gguf"; s.SpeculativeTokens = 33 }},
		{"ngram oversized", func(s *Settings) { s.SpeculativeMode = "ngram"; s.SpeculativeTokens = 33 }},
		{"invalid host gpu", func(s *Settings) { s.HostGPU = "cuda" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := base
			tt.edit(&s)
			if err := m.validate(s); err == nil {
				t.Fatalf("accepted invalid tuning: %+v", s)
			}
		})
	}
}

package dashboard

import (
	"errors"

	"github.com/rfdetoni/nodexlm/internal/local"
)

// Zero prompt-cache budget intentionally disables the separate native RAM
// cache. The active sequence KV and ordinary prefix reuse remain available.
func validateInferenceResources(s Settings) error {
	if s.ParallelSlots < 0 || s.ParallelSlots > 8 || s.ContextTokens > 262144/max(1, s.ParallelSlots) {
		return errors.New("parallel_slots must be 0..8 and total context at most 262144")
	}
	if s.ParallelSlots > 1 && (s.Distributed || s.SpeculativeMode != "" && s.SpeculativeMode != "off" || local.EffectiveBackend(s.Backend) != local.BackendLlama) {
		return errors.New("parallel slots currently require local llama.cpp without speculation")
	}
	if s.ThreadsBatch < 0 || s.ThreadsBatch > 256 || s.PromptCacheMiB < 0 || s.PromptCacheMiB > 8192 {
		return errors.New("threads_batch must be 0..256 and prompt_cache_mib must be 0..8192")
	}
	switch s.LoadMode {
	case "", "auto", "mmap", "none", "mlock", "mmap+mlock", "dio":
	default:
		return errors.New("invalid load_mode")
	}
	switch s.LazyMode {
	case "", "auto", "on", "off":
	default:
		return errors.New("invalid lazy_mode")
	}
	switch s.NUMA {
	case "", "distribute", "isolate", "numactl":
	default:
		return errors.New("invalid numa policy")
	}
	if s.FlashAttention == "off" && (s.KVCacheType == "q8_0" || s.KVCacheType == "q4_0") {
		return errors.New("quantized V cache requires Flash Attention; select auto or on")
	}
	if s.Distributed && (s.LoadMode == "none" || s.LoadMode == "dio" || s.LoadMode == "mlock") {
		return errors.New("distributed loading requires an mmap-compatible load_mode")
	}
	if local.EffectiveBackend(s.Backend) == local.BackendMistralRS &&
		(s.ThreadsBatch != 0 || s.PromptCacheMiB != 0 || s.LoadMode != "" && s.LoadMode != "auto" || s.LazyMode != "" && s.LazyMode != "auto" || s.NUMA != "") {
		return errors.New("selected inference resource options require llama.cpp")
	}
	return nil
}

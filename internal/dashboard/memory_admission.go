package dashboard

import (
	"errors"
	"fmt"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
)

// estimateTransientBytes is deliberately a lower-bound estimate of KV and
// minimum activation buffers. Architectures with unknown KV layout return nil.
func estimateTransientBytes(meta local.GGUFInfo, s Settings) *uint64 {
	if s.ContextTokens <= 0 || meta.BlockCount == 0 || meta.BlockCount > 4096 ||
		meta.EmbeddingLength == 0 || meta.EmbeddingLength > 1<<20 ||
		meta.HeadCount == 0 || meta.KVHeadCount == 0 ||
		meta.KVHeadCount > meta.HeadCount || meta.EmbeddingLength%meta.HeadCount != 0 {
		return nil
	}
	width := uint64(meta.EmbeddingLength/meta.HeadCount) * uint64(meta.KVHeadCount)
	kv := uint64(4) * uint64(s.ContextTokens) * uint64(meta.BlockCount) * width
	switch s.KVCacheType {
	case "q8_0":
		kv = (uint64(2)*uint64(s.ContextTokens)*uint64(meta.BlockCount)*width*34 + 31) / 32
	case "q4_0":
		kv = (uint64(2)*uint64(s.ContextTokens)*uint64(meta.BlockCount)*width*18 + 31) / 32
	}
	batch := s.BatchSize
	if batch == 0 {
		batch = 512 // Minimum provisional allowance, not the native full footprint.
	}
	if s.UBatchSize > batch {
		batch = s.UBatchSize
	}
	activation := uint64(batch) * uint64(meta.EmbeddingLength) * 4
	estimated := kv + activation
	return &estimated
}

// The per-node weight budget already reserves 20% of post-OS memory for
// temporary buffers. Charge only context/batch estimates in EXCESS of that
// reserve, avoiding duplicate deductions for ordinary contexts.
func validatePlacementMemory(meta local.GGUFInfo, s Settings, modelBytes, hostBudget uint64, guests []clusterplan.Candidate) error {
	estimate := estimateTransientBytes(meta, s)
	if estimate == nil {
		if s.ContextTokens > 4096 || s.BatchSize > 1024 || s.UBatchSize > 1024 {
			return errors.New("high context/batch requires GGUF KV metadata to validate cluster memory")
		}
		return nil
	}
	total := hostBudget
	for _, guest := range guests {
		if guest.CapacityBytes > ^uint64(0)-total {
			return errors.New("node memory budget total overflow")
		}
		total += guest.CapacityBytes
	}
	if modelBytes > ^uint64(0)/115 {
		return errors.New("GGUF weight size exceeds memory planner range")
	}
	required := modelBytes * 115 / 100
	genericReserve := total / 4
	if *estimate > genericReserve {
		extra := *estimate - genericReserve
		if extra > ^uint64(0)-required {
			return errors.New("context reserve exceeds planner range")
		}
		required += extra
	}
	if required > total {
		return fmt.Errorf("estimated context/batch memory plus model weights needs %d MiB, selected nodes offer %d MiB; reduce context/batch or add RAM",
			(required+clusterplan.MiB-1)/clusterplan.MiB, total/clusterplan.MiB)
	}
	return nil
}

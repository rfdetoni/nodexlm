package dashboard

import (
	"errors"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// resolveDraftModel ensures that a draft cannot refer to a path outside
// the managed GGUF library or to a missing shard.
func (m *Manager) resolveDraftModel(s Settings) (string, error) {
	if s.SpeculativeMode != "draft" {
		return "", nil
	}
	if s.DraftModel == "" || s.DraftModel == s.Model {
		return "", errors.New("draft model must differ from the main model")
	}
	path, err := modelPath(m.modelDir, s.DraftModel)
	if err != nil {
		return "", err
	}
	if _, err = local.ModelSize(path); err != nil {
		return "", err
	}
	return path, nil
}

// Host-only allocations cannot be funded by remote or dedicated GPU memory.
// Subtract draft weights, draft KV/graph and the separate prompt cache before
// placement so the same budget is not promised to two models.
func (m *Manager) hostBudget(s Settings, host protocol.Device) (uint64, error) {
	total := clusterplan.Budget(host)
	ramHost := host
	ramHost.GPUs = nil
	ramHost.Runtime.RPCDevices = nil
	ram := clusterplan.Budget(ramHost)
	required := uint64(s.PromptCacheMiB) * clusterplan.MiB
	path, err := m.resolveDraftModel(s)
	if err != nil {
		return 0, err
	}
	if path != "" {
		size, err := local.ModelSize(path)
		if err != nil {
			return 0, err
		}
		meta, err := local.InspectGGUF(path)
		if err != nil {
			return 0, err
		}
		draft := s
		draft.KVCacheType = "f16"
		draft.ParallelSlots = 1
		transient := estimateTransientBytes(meta, draft)
		if transient == nil {
			return 0, errors.New("draft requires known KV metadata for memory admission")
		}
		if uint64(size) > ^uint64(0)/115 {
			return 0, errors.New("draft model exceeds planner range")
		}
		weights := uint64(size) * 115 / 100
		if weights > ^uint64(0)-required || *transient > ^uint64(0)-required-weights {
			return 0, errors.New("draft allocation exceeds planner range")
		}
		required += weights + *transient
	}
	if required > ram || required > total {
		return 0, errors.New("insufficient Host RAM for draft model and prompt cache")
	}
	// validatePlacementMemory charges the prompt cache itself; subtract only
	// the draft here to prevent double charging while enforcing Host RAM.
	return total - (required - uint64(s.PromptCacheMiB)*clusterplan.MiB), nil
}

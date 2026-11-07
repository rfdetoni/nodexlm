package dashboard

import (
	"errors"
	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/local"
)

type placement struct {
	Endpoint    string `json:"endpoint"`
	Layers      int    `json:"layers"`
	WeightBytes uint64 `json:"estimated_weight_bytes"`
	Budget      uint64 `json:"budget_bytes"`
}

func (m *Manager) Plan() (map[string]any, error) {
	m.mu.Lock()
	settings := m.settings
	provider := m.rpcEndpoints
	m.mu.Unlock()
	path, err := modelPath(m.modelDir, settings.Model)
	if err != nil {
		return nil, err
	}
	meta, err := local.InspectGGUF(path)
	if err != nil {
		return nil, err
	}
	size, err := local.ModelSize(path)
	if err != nil {
		return nil, err
	}
	if meta.BlockCount == 0 {
		return nil, errors.New("GGUF block_count is unavailable")
	}
	host := clusterplan.Budget(hardware.Collect("Host", nil))
	nodes := []placement{}
	localLayers := int(meta.BlockCount)
	if settings.Distributed {
		if provider == nil {
			return nil, errors.New("RPC provider unavailable")
		}
		guests := provider()
		budgets := []uint64{}
		var total uint64
		for _, guest := range guests {
			budgets = append(budgets, guest.CapacityBytes)
			total += guest.CapacityBytes
		}
		localLayers, err = clusterplan.LocalLayerCount(meta.BlockCount, uint64(size), host, budgets)
		if err != nil {
			return nil, err
		}
		remaining := int(meta.BlockCount) - localLayers
		for i, guest := range guests {
			layers := remaining
			if i < len(guests)-1 {
				layers = int(uint64(remaining) * guest.CapacityBytes / total)
			}
			nodes = append(nodes, placement{guest.Endpoint, layers, uint64(size) * uint64(layers) / uint64(meta.BlockCount), guest.CapacityBytes})
			remaining -= layers
			total -= guest.CapacityBytes
		}
	} else if uint64(size) > host {
		return nil, errors.New("model weights exceed reported Host budget")
	}
	nodes = append([]placement{{"Host", localLayers, uint64(size) * uint64(localLayers) / uint64(meta.BlockCount), host}}, nodes...)
	return map[string]any{"model": settings.Model, "size_bytes": size, "blocks": meta.BlockCount, "nodes": nodes, "tokens_per_second": nil, "notice": "Layer counts and weight memory are estimates. Native GGML selects remote allocation; KV cache and working buffers require additional memory. Tokens/s requires a model inference benchmark; network echo alone cannot predict it."}, nil
}

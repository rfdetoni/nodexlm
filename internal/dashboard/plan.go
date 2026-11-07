package dashboard

import (
	"encoding/json"
	"errors"
	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
	"os"
	"path/filepath"
	"time"
)

type placement struct {
	TokensPerSecond *float64             `json:"tokens_per_second,omitempty"`
	NodeID          string               `json:"node_id,omitempty"`
	Name            string               `json:"name"`
	Network         *rpcmesh.Measurement `json:"network,omitempty"`
	Endpoint        string               `json:"endpoint"`
	Layers          int                  `json:"layers"`
	WeightBytes     uint64               `json:"estimated_weight_bytes"`
	Budget          uint64               `json:"budget_bytes"`
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
			nodes = append(nodes, placement{NodeID: guest.ID, Name: guest.Name, Network: m.networkSample(guest.ID), Endpoint: guest.Endpoint, Layers: layers, WeightBytes: uint64(size) * uint64(layers) / uint64(meta.BlockCount), Budget: guest.CapacityBytes})
			remaining -= layers
			total -= guest.CapacityBytes
		}
	} else if uint64(size) > host {
		return nil, errors.New("model weights exceed reported Host budget")
	}
	nodes = append([]placement{{Name: "Host", Endpoint: "Host", Layers: localLayers, WeightBytes: uint64(size) * uint64(localLayers) / uint64(meta.BlockCount), Budget: host}}, nodes...)

	var kvBytes *uint64
	if meta.EmbeddingLength > 0 && meta.HeadCount > 0 && meta.KVHeadCount > 0 && meta.KVHeadCount <= meta.HeadCount && meta.BlockCount <= 4096 && meta.EmbeddingLength <= 1<<20 {
		// F16 K and V tensors; architectures with other KV layouts remain unknown.
		value := uint64(4) * uint64(settings.ContextTokens) * uint64(meta.BlockCount) * uint64(meta.EmbeddingLength) * uint64(meta.KVHeadCount) / uint64(meta.HeadCount)
		kvBytes = &value
	}
	samples := m.InferenceSamples()
	for i := range nodes {
		key := settings.Model
		if nodes[i].NodeID != "" {
			key += "@" + nodes[i].NodeID
		}
		for _, sample := range samples {
			if sample.Model == key && time.Since(sample.MeasuredAt) < 24*time.Hour {
				rate := sample.TokensPerSecond
				nodes[i].TokensPerSecond = &rate
			}
		}
	}
	var tps *float64
	for _, sample := range samples {
		if sample.Model == settings.Model && time.Since(sample.MeasuredAt) < 24*time.Hour {
			rate := sample.TokensPerSecond
			tps = &rate
		}
	}
	bottleneck := "unmeasured"
	for _, node := range nodes {
		if node.Network != nil && (node.Network.RoundTripMS > 10 || node.Network.MiBPerSecond < 50) {
			bottleneck = "network-advisory"
		}
	}
	if uint64(size) > host {
		bottleneck = "host-memory"
	}
	return map[string]any{"model": settings.Model, "size_bytes": size, "blocks": meta.BlockCount, "nodes": nodes, "estimated_kv_bytes": kvBytes, "tokens_per_second": tps, "bottleneck": bottleneck, "notice": "Layer placement and F16 KV memory are estimates; native GGML determines actual allocation. Network advisory thresholds: RTT >10 ms or aggregate echo <50 MiB/s. Tokens/s is an observed measurement for this model, not a prediction from bandwidth; configuration or node changes can invalidate comparison."}, nil
}
func (m *Manager) networkSample(id string) *rpcmesh.Measurement {
	if len(id) != 64 {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(m.dir, "bench-"+id+".json"))
	if err != nil {
		return nil
	}
	var sample rpcmesh.Measurement
	if json.Unmarshal(raw, &sample) != nil || sample.NodeID != id || time.Since(sample.MeasuredAt) > 24*time.Hour {
		return nil
	}
	return &sample
}

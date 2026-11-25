package dashboard

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

const speculativeRTTThresholdMS = 5.0

type planSuggestion struct {
	Kind        string  `json:"kind"`
	Reason      string  `json:"reason"`
	RoundTripMS float64 `json:"round_trip_ms,omitempty"`
}

func speculativeSuggestion(nodes []placement) []planSuggestion {
	var rtt float64
	for _, node := range nodes {
		if node.NodeID != "" && node.Layers > 0 && node.Network != nil {
			rtt = max(rtt, node.Network.RoundTripMS)
		}
	}
	if rtt < speculativeRTTThresholdMS {
		return nil
	}
	return []planSuggestion{{Kind: "speculative_ngram", RoundTripMS: rtt,
		Reason: "RTT medido elevado: testar n-gram com autotuning pode amortizar round-trips; não há ganho garantido."}}
}

type placement struct {
	TokensPerSecond *float64             `json:"tokens_per_second,omitempty"`
	NodeID          string               `json:"node_id,omitempty"`
	RPCMode         string               `json:"rpc_mode,omitempty"`
	RAMBudget       uint64               `json:"ram_budget_bytes,omitempty"`
	VRAMBudget      uint64               `json:"vram_budget_bytes,omitempty"`
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
	host, err := m.hostBudget(settings, hardware.Collect("Host", nil))
	if err != nil {
		return nil, err
	}
	nodes := []placement{}
	localLayers := int(meta.BlockCount)
	hostWeight := uint64(size)
	strategy := effectivePlacementStrategy(settings.PlacementStrategy)
	excluded := []string{}
	var suggestions []planSuggestion
	selectionReason := "Local-only inference"
	if settings.Distributed {
		var guests []RPCGuest
		if provider != nil {
			guests = provider()
		}
		measurements := m.placementMeasurements(settings)
		selection, selectErr := m.chooseRPCWithMeasurements(settings, meta, uint64(size), host, guests, measurements)
		if selectErr != nil {
			return nil, selectErr
		}
		localLayers = selection.LocalLayers
		hostWeight = uint64(size) * uint64(localLayers) / uint64(meta.BlockCount)
		if len(selection.GuestLayers) > 0 && len(selection.GuestLayers) == len(selection.Guests) && len(meta.BlockBytes) == int(meta.BlockCount) {
			hostWeight = meta.NonBlockBytes + blockWeight(meta, 0, localLayers)
		}
		devices := planRPCDevices(selection, guests, meta, settings, uint64(size))
		suggestions = append(suggestions, m.kvPlacementSuggestion(settings, meta, uint64(size), host, guests, selection, devices, measurements)...)
		excluded = selection.Excluded
		selectionReason = selection.Reason
		var total uint64
		for _, guest := range selection.Guests {
			total += guest.CapacityBytes
		}
		remaining := int(meta.BlockCount) - localLayers
		for i, guest := range selection.Guests {
			layers := remaining
			if i < len(selection.Guests)-1 {
				layers = int(uint64(remaining) * guest.CapacityBytes / total)
			}
			if len(selection.GuestLayers) == len(selection.Guests) {
				layers = selection.GuestLayers[i]
			}
			weight := uint64(size) * uint64(layers) / uint64(meta.BlockCount)
			if len(selection.GuestLayers) == len(selection.Guests) {
				weight = estimatedBlockWeight(meta, uint64(size), int(meta.BlockCount)-remaining, layers)
			}
			name, endpoint := guest.ID, ""
			var ramBudget, vramBudget uint64
			for _, original := range guests {
				if original.ID == guest.ID {
					name, endpoint = original.Name, original.Endpoint
					ramBudget, vramBudget = original.RAMBudgetBytes, original.GPUBudgetBytes
					break
				}
			}
			nodes = append(nodes, placement{NodeID: guest.ID, Name: name, Network: m.networkSample(guest.ID),
				Endpoint: endpoint, Layers: layers, WeightBytes: weight,
				Budget: guest.CapacityBytes, RPCMode: devices.Modes[guest.ID],
				RAMBudget: ramBudget, VRAMBudget: vramBudget})
			remaining -= layers
			total -= guest.CapacityBytes
		}
	} else {
		if uint64(size) > host {
			return nil, errors.New("model weights exceed reported Host budget")
		}
		if err := validatePlacementMemory(meta, settings, uint64(size), host, nil); err != nil {
			return nil, err
		}
	}
	nodes = append([]placement{{Name: "Host", Endpoint: "Host", Layers: localLayers, WeightBytes: hostWeight, Budget: host}}, nodes...)

	kvBytes := estimateKVBytes(meta, settings)
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
	kvType := settings.KVCacheType
	if kvType == "" {
		kvType = "f16"
	}
	return map[string]any{"suggestions": append(suggestions, speculativeSuggestion(nodes)...), "estimated_minimum_runtime_bytes": estimateTransientBytes(meta, settings), "bundled_rpc_release": llamaruntime.BundledRPCRelease(), "bundled_parallel_rpc_uploads_supported": llamaruntime.BundledParallelRPCUploadsSupported(), "estimated_kv_type": kvType, "strategy": strategy, "primary_node": settings.PrimaryNode, "excluded_guests": excluded, "selection_reason": selectionReason, "model": settings.Model, "size_bytes": size, "blocks": meta.BlockCount, "nodes": nodes, "estimated_kv_bytes": kvBytes, "tokens_per_second": tps, "bottleneck": bottleneck, "notice": "Layer placement and KV memory are estimates; native GGML determines actual allocation. Network advisory thresholds: RTT >10 ms or aggregate echo <50 MiB/s. Tokens/s is an observed measurement for this model, not a prediction from bandwidth; configuration or node changes can invalidate comparison."}, nil
}
func (m *Manager) networkSample(id string) *rpcmesh.Measurement {
	if len(id) != 64 {
		return nil
	}
	if _, err := hex.DecodeString(id); err != nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(m.dir, "bench-"+id+".json"))
	if err != nil {
		return nil
	}
	var sample rpcmesh.Measurement
	if json.Unmarshal(raw, &sample) != nil || sample.NodeID != id || sample.RoundTripMS < 0 || sample.MiBPerSecond <= 0 || sample.MeasuredAt.After(time.Now()) || time.Since(sample.MeasuredAt) >= 24*time.Hour {
		return nil
	}
	return &sample
}

func (m *Manager) kvPlacementSuggestion(s Settings, meta local.GGUFInfo, size, host uint64, guests []RPCGuest, original clusterplan.Selection, devices RPCDevicePlacement, measurements placementMeasurements) []planSuggestion {
	if (s.KVCacheType != "" && s.KVCacheType != "f16") || estimateKVBytes(meta, s) == nil {
		return nil
	}
	s.KVCacheType = "q8_0"
	// Retain the original controlled rates only for this memory what-if.
	// Do not persist them or treat them as measured q8_0 throughput.
	alternative, err := m.chooseRPCWithMeasurements(s, meta, size, host, guests, measurements)
	if err != nil {
		return nil
	}
	optimized := planRPCDevices(alternative, guests, meta, s, size)
	improved := len(alternative.Guests) < len(original.Guests)
	for id, mode := range devices.Modes {
		if mode == "gpu+ram" && optimized.Modes[id] == "gpu" {
			improved = true
		}
	}
	if !improved {
		return nil
	}
	return []planSuggestion{{Kind: "kv_q8_0", Reason: "Estimativa de admissão com KV q8_0 (mesmas medições de placement) permite reduzir Guests ou usar somente GPU. Teste Flash Attention compatível e qualidade antes de aplicar."}}
}

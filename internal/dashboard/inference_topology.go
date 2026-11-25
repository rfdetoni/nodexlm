// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

type topologyNode struct {
	NodeID       string   `json:"node_id"`
	Name         string   `json:"name"`
	Layers       int      `json:"layers"`
	FirstBlock   int      `json:"first_block"`
	LastBlock    int      `json:"last_block"`
	WeightBytes  uint64   `json:"weight_bytes"`
	WeightMethod string   `json:"weight_method"`
	KVBytes      *uint64  `json:"estimated_kv_bytes,omitempty"`
	Mode         string   `json:"mode"`
	Devices      []string `json:"devices,omitempty"`
	RAMBudget    uint64   `json:"ram_budget_bytes"`
	VRAMBudget   uint64   `json:"vram_budget_bytes"`
}

type inferenceTopology struct {
	Loaded        bool           `json:"loaded"`
	Model         string         `json:"model,omitempty"`
	Blocks        int            `json:"blocks"`
	ParallelSlots int            `json:"parallel_slots"`
	Aggregated    bool           `json:"aggregated"`
	Nodes         []topologyNode `json:"nodes"`
	NativeDevices []string       `json:"native_devices,omitempty"`
	TensorSplit   string         `json:"tensor_split,omitempty"`
	LocalLayers   int            `json:"rpc_local_layers"`
	Notice        string         `json:"notice"`
}

// This snapshot is published only after engine.Load succeeds. Native args are
// factual, block boundaries reconstructed from split ratios are estimates.
func buildInferenceTopology(s Settings, meta local.GGUFInfo, size, hostBudget uint64, selected clusterplan.Selection, guests []RPCGuest, devices RPCDevicePlacement, engine *local.Managed) *inferenceTopology {
	blocks := int(meta.BlockCount)
	out := &inferenceTopology{Loaded: true, Model: s.Model, Blocks: blocks, ParallelSlots: max(1, s.ParallelSlots), Aggregated: s.ParallelSlots > 1, NativeDevices: append([]string(nil), engine.RPCDeviceNames...), TensorSplit: engine.RPCTensorSplit, LocalLayers: engine.RPCLocalLayers, Notice: "Argumentos do runtime carregado; faixas e bytes por nó estimados pela divisão de camadas, não telemetria nativa por bloco."}
	host := topologyNode{NodeID: "host", Name: "Host", Mode: "host-local", RAMBudget: hostBudget}
	if s.GPULayers == "0" || len(engine.RPCServers) > 0 {
		host.Mode = "cpu"
	}
	out.Nodes = append(out.Nodes, host)
	if len(engine.RPCServers) == 0 {
		out.Nodes[0].Layers = blocks
	} else {
		out.Nodes[0].Layers = engine.RPCLocalLayers
		byID := map[string]RPCGuest{}
		for _, guest := range guests {
			byID[guest.ID] = guest
		}
		deviceOwner := map[string]int{}
		rpcIndex := 0
		var total uint64
		for _, chosen := range selected.Guests {
			guest := byID[chosen.ID]
			name := guest.Name
			if name == "" {
				name = guest.ID
			}
			mode := devices.Modes[guest.ID]
			if mode == "ram" {
				mode = "cpu"
			}
			if mode == "" {
				mode = "legacy"
			}
			out.Nodes = append(out.Nodes, topologyNode{NodeID: guest.ID, Name: name, Mode: mode, RAMBudget: guest.RAMBudgetBytes, VRAMBudget: guest.GPUBudgetBytes})
			for range guest.RPCDeviceKinds {
				deviceOwner[fmt.Sprintf("RPC%d", rpcIndex)] = len(out.Nodes) - 1
				rpcIndex++
			}
			total += chosen.CapacityBytes
		}
		if devices.Modes["host"] != "" && len(devices.Devices) > 0 {
			deviceOwner[devices.Devices[0]] = 0
			out.Nodes[0].Mode = "host-" + devices.Modes["host"]
		}
		remaining := blocks - engine.RPCLocalLayers
		if len(devices.Devices) > 0 && len(devices.Devices) == len(devices.Split) && remaining > 0 {
			var sum, cumulative uint64
			for _, weight := range devices.Split {
				sum += weight
			}
			previous := 0
			for i, device := range devices.Devices {
				owner := deviceOwner[device]
				cumulative += devices.Split[i]
				end := int((cumulative*uint64(remaining) + sum - 1) / sum)
				out.Nodes[owner].Layers += end - previous
				out.Nodes[owner].Devices = append(out.Nodes[owner].Devices, device)
				previous = end
			}
		} else {
			// Legacy native device ordering is unknown. Use the admitted
			// layer estimate rather than pretending to observe device mapping.
			out.Notice += " Guest legacy: divisão proporcional estimada; dispositivos nativos desconhecidos."
			for i, chosen := range selected.Guests {
				layers := remaining
				if i < len(selected.Guests)-1 && total > 0 {
					layers = int(uint64(remaining) * chosen.CapacityBytes / total)
				}
				if len(selected.GuestLayers) == len(selected.Guests) {
					layers = selected.GuestLayers[i]
				}
				out.Nodes[i+1].Layers = layers
				remaining -= layers
				total -= chosen.CapacityBytes
			}
		}
	}
	kv := estimateKVBytes(meta, s)
	start := 0
	for i := range out.Nodes {
		node := &out.Nodes[i]
		node.FirstBlock, node.LastBlock = -1, -1
		if node.Layers > 0 && start+node.Layers <= blocks {
			node.FirstBlock, node.LastBlock = start, start+node.Layers-1
		}
		node.WeightMethod = "uniform_estimate"
		if blocks > 0 && node.Layers >= 0 && start+node.Layers <= blocks {
			node.WeightBytes = size / uint64(blocks) * uint64(node.Layers)
			if len(meta.BlockBytes) == blocks {
				node.WeightMethod = "gguf_block_bytes_estimated_assignment"
				node.WeightBytes = blockWeight(meta, start, node.Layers)
				if i == 0 {
					node.WeightBytes += meta.NonBlockBytes
				}
			}
			if kv != nil {
				estimate := *kv / uint64(blocks) * uint64(node.Layers)
				node.KVBytes = &estimate
			}
		}
		if len(engine.RPCServers) == 0 && i == 0 {
			node.WeightBytes, node.WeightMethod = size, "gguf_file_size"
		}
		start += node.Layers
	}
	return out
}

func (m *Manager) InferenceTopology() *inferenceTopology {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadedTopology == nil || m.engine == nil || m.unloading {
		return &inferenceTopology{Nodes: []topologyNode{}}
	}
	return m.loadedTopology
}

const activitySampling = 200 * time.Millisecond
const activityWindowDuration = 2 * time.Second

type activityObservation struct {
	at    time.Time
	stats map[string]rpcmesh.TunnelStats
}

type activityNode struct {
	NodeID         string  `json:"node_id"`
	BytesDelta     uint64  `json:"bytes_delta"`
	WritesDelta    uint64  `json:"writes_delta"`
	SharePercent   float64 `json:"share_percent_window"`
	MiBPerSecond   float64 `json:"mib_per_second_window"`
	LastActivityMS int64   `json:"last_activity_ms"`
	Active         bool    `json:"active"`
	Available      bool    `json:"available"`
}

type inferenceActivity struct {
	InFlight     int                         `json:"in_flight"`
	Phase        string                      `json:"phase"`
	SamplingMS   int64                       `json:"sampling_ms"`
	WindowMS     int64                       `json:"window_ms"`
	HostInferred bool                        `json:"host_inferred"`
	Nodes        []activityNode              `json:"nodes"`
	Notice       string                      `json:"notice"`
	NativeTokens []local.NativeTokenActivity `json:"native_tokens,omitempty"`
}

func activityDelta(a, b rpcmesh.TunnelStats) (bytes, writes uint64, ok bool) {
	if a.Epoch == 0 || a.Epoch != b.Epoch || b.Connected == 0 || b.BytesToGuest < a.BytesToGuest || b.BytesFromGuest < a.BytesFromGuest || b.WritesToGuest < a.WritesToGuest || b.WritesFromGuest < a.WritesFromGuest {
		return 0, 0, false
	}
	sent, received := b.BytesToGuest-a.BytesToGuest, b.BytesFromGuest-a.BytesFromGuest
	writesTo, writesFrom := b.WritesToGuest-a.WritesToGuest, b.WritesFromGuest-a.WritesFromGuest
	if sent > ^uint64(0)-received || writesTo > ^uint64(0)-writesFrom {
		return 0, 0, false
	}
	return sent + received, writesTo + writesFrom, true
}

func calculateActivity(nodes []topologyNode, oldest, previous, current activityObservation) ([]activityNode, bool) {
	out := []activityNode{}
	var total float64
	complete, active := !previous.at.IsZero(), false
	for _, node := range nodes {
		if node.NodeID == "host" {
			continue
		}
		now := current.stats[node.NodeID]
		bytes, writes, ok := activityDelta(previous.stats[node.NodeID], now)
		window, _, windowOK := activityDelta(oldest.stats[node.NodeID], now)
		if !ok {
			complete = false
		}
		item := activityNode{NodeID: node.NodeID, BytesDelta: bytes, WritesDelta: writes, LastActivityMS: now.LastActivityMS, Available: ok, Active: ok && (bytes > 0 || writes > 0)}
		if windowOK && current.at.After(oldest.at) {
			item.MiBPerSecond = float64(window) / 1048576 / current.at.Sub(oldest.at).Seconds()
			item.SharePercent = float64(window)
			total += float64(window)
		}
		active = active || item.Active
		out = append(out, item)
	}
	for i := range out {
		if total > 0 {
			out[i].SharePercent = 100 * out[i].SharePercent / total
		}
	}
	return out, complete && !active
}

// Sample on demand: no background timer or work when no visible client is
// requesting activity, and no counter collection when inference is idle.
func (m *Manager) InferenceActivity() inferenceActivity {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := inferenceActivity{InFlight: len(m.generations), Phase: "idle", SamplingMS: activitySampling.Milliseconds(), Nodes: []activityNode{}, Notice: "Estimativa por amostragem de tráfego RPC; segmentos por token podem ser mais curtos que a amostragem. Host inferido quando não há delta remoto válido."}
	if m.engine != nil {
		out.NativeTokens = m.engine.NativeActivity()
	}
	if out.InFlight == 0 || m.loadedTopology == nil {
		m.activityWindow = nil
		return out
	}
	out.Phase = "unknown"
	phases := map[string]bool{}
	for id := range m.generations {
		phase := m.generationPhases[id]
		if phase == "" {
			phase = "unknown"
		}
		phases[phase] = true
	}
	if len(phases) == 1 {
		for phase := range phases {
			out.Phase = phase
		}
	} else if len(phases) > 1 {
		out.Phase = "mixed"
	}
	now := time.Now()
	if len(m.activityWindow) > 0 && now.Sub(m.activityWindow[len(m.activityWindow)-1].at) > activityWindowDuration {
		m.activityWindow = nil
	}
	if len(m.activityWindow) > 1 && now.Sub(m.activityWindow[len(m.activityWindow)-1].at) < 100*time.Millisecond {
		current := m.activityWindow[len(m.activityWindow)-1]
		out.Nodes, out.HostInferred = calculateActivity(m.loadedTopology.Nodes, m.activityWindow[0], m.activityWindow[len(m.activityWindow)-2], current)
		out.WindowMS = current.at.Sub(m.activityWindow[0].at).Milliseconds()
		return out
	}
	current := activityObservation{at: now}
	if m.rpcStatsProvider != nil {
		current.stats = m.rpcStatsProvider()
	}
	var previous activityObservation
	if len(m.activityWindow) > 0 {
		previous = m.activityWindow[len(m.activityWindow)-1]
	}
	m.activityWindow = append(m.activityWindow, current)
	for len(m.activityWindow) > 1 && (now.Sub(m.activityWindow[0].at) > activityWindowDuration || len(m.activityWindow) > 21) {
		m.activityWindow = m.activityWindow[1:]
	}
	out.Nodes, out.HostInferred = calculateActivity(m.loadedTopology.Nodes, m.activityWindow[0], previous, current)
	out.WindowMS = now.Sub(m.activityWindow[0].at).Milliseconds()
	if len(m.loadedTopology.Nodes) == 1 && strings.EqualFold(m.loadedTopology.Nodes[0].NodeID, "host") {
		out.HostInferred = true
	}
	return out
}

package dashboard

import (
	"time"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

// Write counts describe successful relay writes, not protocol calls or RTT.
// Request bytes include prefill as well as decode, excluding model load.
type nodeInferenceTraffic struct {
	NodeID string `json:"node_id"`
	Name   string `json:"name"`
	Bytes  uint64 `json:"bytes"`

	EstimatedLayers *int `json:"estimated_layers,omitempty"`

	// Bytes and writes are completed relay transfers, not native GPU work.
	BytesToGuest            uint64  `json:"bytes_to_guest"`
	BytesFromGuest          uint64  `json:"bytes_from_guest"`
	WritesToGuest           uint64  `json:"writes_to_guest"`
	WritesFromGuest         uint64  `json:"writes_from_guest"`
	ToGuestBytesPerSecond   float64 `json:"to_guest_bytes_per_second,omitempty"`
	FromGuestBytesPerSecond float64 `json:"from_guest_bytes_per_second,omitempty"`
}

type InferenceRPCTraffic struct {
	Nodes                []nodeInferenceTraffic `json:"nodes,omitempty"`
	HostLayers           int                    `json:"estimated_host_layers"`
	BytesToGuest         uint64                 `json:"bytes_to_guest"`
	BytesFromGuest       uint64                 `json:"bytes_from_guest"`
	WritesToGuest        uint64                 `json:"writes_to_guest"`
	WritesFromGuest      uint64                 `json:"writes_from_guest"`
	BytesPerOutput       float64                `json:"request_bytes_per_output_token"`
	RequestWindowSeconds float64                `json:"request_window_seconds,omitempty"`

	// SSE-observed boundary, not an exact native phase.
	PrefillWindow *InferenceRPCTraffic `json:"until_first_event,omitempty"`
	DecodeWindow  *InferenceRPCTraffic `json:"after_first_event,omitempty"`
}

func inferenceTraffic(nodes []string, before, after map[string]rpcmesh.TunnelStats) *InferenceRPCTraffic {
	if len(nodes) == 0 {
		return nil
	}
	result := &InferenceRPCTraffic{}
	for _, id := range nodes {
		a, aok := before[id]
		b, bok := after[id]
		if !aok || !bok || a.Epoch == 0 || a.Epoch != b.Epoch ||
			b.BytesToGuest < a.BytesToGuest || b.BytesFromGuest < a.BytesFromGuest ||
			b.WritesToGuest < a.WritesToGuest || b.WritesFromGuest < a.WritesFromGuest {
			return nil
		}
		toGuest := b.BytesToGuest - a.BytesToGuest
		fromGuest := b.BytesFromGuest - a.BytesFromGuest
		writeTo := b.WritesToGuest - a.WritesToGuest
		writeFrom := b.WritesFromGuest - a.WritesFromGuest
		result.Nodes = append(result.Nodes, nodeInferenceTraffic{
			NodeID: id, Name: id, Bytes: toGuest + fromGuest,
			BytesToGuest: toGuest, BytesFromGuest: fromGuest,
			WritesToGuest: writeTo, WritesFromGuest: writeFrom,
		})
		result.BytesToGuest += toGuest
		result.BytesFromGuest += fromGuest
		result.WritesToGuest += writeTo
		result.WritesFromGuest += writeFrom
	}
	return result
}

func (m *Manager) recordRPCTraffic(model string, start time.Time, nodes []string, before, after map[string]rpcmesh.TunnelStats) *InferenceRPCTraffic {
	traffic := inferenceTraffic(nodes, before, after)
	if traffic == nil {
		return nil
	}
	// This window includes the request's prefill and decode. Dividing by it
	// does not claim per-device kernel throughput or per-token RPC latency.
	window := time.Since(start).Seconds()
	if window > 0 {
		traffic.RequestWindowSeconds = window
		for i := range traffic.Nodes {
			traffic.Nodes[i].ToGuestBytesPerSecond = float64(traffic.Nodes[i].BytesToGuest) / window
			traffic.Nodes[i].FromGuestBytesPerSecond = float64(traffic.Nodes[i].BytesFromGuest) / window
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loadedTopology != nil {
		for _, node := range m.loadedTopology.Nodes {
			if node.NodeID == "host" {
				traffic.HostLayers = node.Layers
			}
			for i := range traffic.Nodes {
				if traffic.Nodes[i].NodeID == node.NodeID {
					traffic.Nodes[i].Name = node.Name
					layers := node.Layers
					traffic.Nodes[i].EstimatedLayers = &layers
				}
			}
		}
	}
	key := model + "@cluster"
	sample, ok := m.samples[key]
	if !ok || sample.MeasuredAt.Before(start) || sample.Tokens <= 0 {
		return traffic
	}
	traffic.BytesPerOutput = (float64(traffic.BytesToGuest) + float64(traffic.BytesFromGuest)) / float64(sample.Tokens)
	sample.RPCTraffic = traffic
	m.samples[key] = sample
	return traffic
}

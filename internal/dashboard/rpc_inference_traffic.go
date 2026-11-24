package dashboard

import (
	"time"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

// Write counts describe successful relay writes, not protocol calls or RTT.
// Request bytes include prefill as well as decode, excluding model load.
type InferenceRPCTraffic struct {
	BytesToGuest    uint64  `json:"bytes_to_guest"`
	BytesFromGuest  uint64  `json:"bytes_from_guest"`
	WritesToGuest   uint64  `json:"writes_to_guest"`
	WritesFromGuest uint64  `json:"writes_from_guest"`
	BytesPerOutput  float64 `json:"request_bytes_per_output_token"`
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
		result.BytesToGuest += b.BytesToGuest - a.BytesToGuest
		result.BytesFromGuest += b.BytesFromGuest - a.BytesFromGuest
		result.WritesToGuest += b.WritesToGuest - a.WritesToGuest
		result.WritesFromGuest += b.WritesFromGuest - a.WritesFromGuest
	}
	return result
}

func (m *Manager) recordRPCTraffic(model string, start time.Time, nodes []string, before, after map[string]rpcmesh.TunnelStats) {
	traffic := inferenceTraffic(nodes, before, after)
	if traffic == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := model + "@cluster"
	sample, ok := m.samples[key]
	if !ok || sample.MeasuredAt.Before(start) || sample.Tokens <= 0 {
		return
	}
	traffic.BytesPerOutput = (float64(traffic.BytesToGuest) + float64(traffic.BytesFromGuest)) / float64(sample.Tokens)
	sample.RPCTraffic = traffic
	m.samples[key] = sample
}

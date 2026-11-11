package dashboard

import (
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
)

// chooseRPC is used both for preview and actual native model loading.
func (m *Manager) chooseRPC(s Settings, blocks uint32, size uint64, hostBudget uint64, guests []RPCGuest) (clusterplan.Selection, error) {
	samples := m.InferenceSamples()
	candidates := make([]clusterplan.Candidate, 0, len(guests))
	now := time.Now()
	for _, guest := range guests {
		candidate := clusterplan.Candidate{ID: guest.ID, CapacityBytes: guest.CapacityBytes}
		if network := m.networkSample(guest.ID); network != nil {
			candidate.Performance.NetworkMeasured = true
			candidate.Performance.RoundTripMS = network.RoundTripMS
			candidate.Performance.MiBPerSecond = network.MiBPerSecond
		}
		for _, sample := range samples {
			if sample.Model == s.Model+"@"+guest.ID && !sample.MeasuredAt.After(now) && now.Sub(sample.MeasuredAt) < 24*time.Hour && sample.TokensPerSecond > 0 {
				candidate.Performance.TokensPerSecond = sample.TokensPerSecond
				break
			}
		}
		candidates = append(candidates, candidate)
	}
	return clusterplan.Select(blocks, size, hostBudget, candidates, s.PlacementStrategy)
}

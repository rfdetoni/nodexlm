package dashboard

import (
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
)

// chooseRPC is used both for preview and actual native model loading.
func (m *Manager) chooseRPC(s Settings, meta local.GGUFInfo, size uint64, hostBudget uint64, guests []RPCGuest) (clusterplan.Selection, error) {
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
	selection, err := clusterplan.Select(meta.BlockCount, size, hostBudget, candidates, s.PlacementStrategy)
	if err != nil {
		return selection, err
	}
	if err = validatePlacementMemory(meta, s, size, hostBudget, selection.Guests); err == nil {
		return selection, nil
	}
	if s.PlacementStrategy != "speed" {
		return clusterplan.Selection{}, err
	}
	// A speed-optimized subset can fit the weights but not the KV/context.
	// Preserve correctness by retrying placement with all healthy Guests.
	full, fullErr := clusterplan.Select(meta.BlockCount, size, hostBudget, candidates, "capacity")
	if fullErr != nil {
		return clusterplan.Selection{}, fullErr
	}
	if contextErr := validatePlacementMemory(meta, s, size, hostBudget, full.Guests); contextErr != nil {
		return clusterplan.Selection{}, contextErr
	}
	full.Reason = "Context/batch memory requires additional RPC Guests; speed subset rejected"
	return full, nil
}

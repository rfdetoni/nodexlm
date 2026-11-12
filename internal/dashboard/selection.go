package dashboard

import (
	"errors"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
)

// chooseRPC is used both for preview and actual native model loading.
func (m *Manager) chooseRPC(s Settings, meta local.GGUFInfo, size uint64, hostBudget uint64, guests []RPCGuest) (clusterplan.Selection, error) {
	samples := m.InferenceSamples()
	now := time.Now()
	// Speed is an automatic preference, unlike capacity's explicit RPC
	// distribution. A Host-only placement is valid when the entire working
	// set fits locally. Prefer a measured faster Guest when one exists.
	if s.PlacementStrategy == "speed" &&
		validatePlacementMemory(meta, s, size, hostBudget, nil) == nil &&
		meta.BlockCount > 0 && size > 0 {
		var hostSpeed, bestGuestSpeed float64
		knownGuests := make(map[string]bool, len(guests))
		for _, guest := range guests {
			knownGuests[guest.ID] = true
		}
		for _, sample := range samples {
			if sample.TokensPerSecond <= 0 || sample.MeasuredAt.After(now) ||
				now.Sub(sample.MeasuredAt) >= 24*time.Hour {
				continue
			}
			if sample.Model == s.Model {
				hostSpeed = sample.TokensPerSecond
			} else if len(sample.Model) > len(s.Model)+1 &&
				sample.Model[:len(s.Model)+1] == s.Model+"@" &&
				knownGuests[sample.Model[len(s.Model)+1:]] &&
				sample.TokensPerSecond > bestGuestSpeed {
				bestGuestSpeed = sample.TokensPerSecond
			}
		}
		if bestGuestSpeed == 0 || (hostSpeed > 0 && hostSpeed >= bestGuestSpeed) {
			excluded := make([]string, 0, len(guests))
			for _, guest := range guests {
				excluded = append(excluded, guest.ID)
			}
			reason := "Host-only memory admission passed; no comparable faster Guest measurement. Local-first is a latency heuristic, not a measured speed claim"
			if hostSpeed > 0 && bestGuestSpeed > 0 {
				reason = "Host-only measured throughput is at least as high as available Guest benchmarks for this model"
			}
			return clusterplan.Selection{LocalLayers: int(meta.BlockCount), Excluded: excluded, Reason: reason}, nil
		}
	}
	candidates := make([]clusterplan.Candidate, 0, len(guests))
	unavailable := make([]string, 0)
	seen := make(map[string]bool, len(guests))
	for _, guest := range guests {
		if guest.ID == "" || seen[guest.ID] {
			return clusterplan.Selection{}, errors.New("duplicate or missing RPC Guest ID")
		}
		seen[guest.ID] = true
		if guest.CapacityBytes == 0 {
			// A connected Android may advertise 0 allocatable RAM during
			// memory pressure. It cannot be selected but should not prevent
			// other healthy Guests from contributing their free capacity.
			unavailable = append(unavailable, guest.ID)
			continue
		}
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
		selection.Excluded = append(selection.Excluded, unavailable...)
		if len(unavailable) > 0 {
			selection.Reason += "; excluded Guests with no allocatable RAM"
		}
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
	full.Excluded = append(full.Excluded, unavailable...)
	return full, nil
}

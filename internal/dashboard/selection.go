package dashboard

import (
	"errors"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
)

// effectivePlacementStrategy uses low-latency execution unless the user
// explicitly opts into capacity mode. Empty legacy settings are speed-first.
func effectivePlacementStrategy(value string) string {
	if value == "" {
		return "speed"
	}
	return value
}

// chooseRPC is used both for preview and actual native model loading.
func (m *Manager) chooseRPC(s Settings, meta local.GGUFInfo, size uint64, hostBudget uint64, guests []RPCGuest) (clusterplan.Selection, error) {
	samples := m.InferenceSamples()
	now := time.Now()
	strategy := effectivePlacementStrategy(s.PlacementStrategy)
	// When the complete GGUF and context fit on the Host, split execution
	// incurs extra RPC synchronization on EVERY decoded token. A remote
	// benchmark may only beat Host-only when both were measured with the same
	// benchmark and the chosen remote can fit the whole model independently.
	// Never compare an unmeasured Host with a measured Guest and assume RPC
	// is faster; and never infer split-cluster speed from Guest-only metrics.
	if strategy == "speed" && size > 0 && size <= ^uint64(0)/115 &&
		size*115/100 <= hostBudget &&
		validatePlacementMemory(meta, s, size, hostBudget, nil) == nil &&
		meta.BlockCount > 0 {
		var hostSpeed float64
		var hostBasis string
		guestSpeed := make(map[string]float64, len(guests))
		guestBasis := make(map[string]string, len(guests))
		for _, sample := range samples {
			if sample.Kind != "benchmark" || sample.TokensPerSecond <= 0 ||
				sample.MeasuredAt.After(now) || now.Sub(sample.MeasuredAt) >= 24*time.Hour {
				continue
			}
			if sample.Model == s.Model {
				hostSpeed = sample.TokensPerSecond
				hostBasis = sample.RateBasis
			}
			for _, guest := range guests {
				if sample.Model == s.Model+"@"+guest.ID {
					guestSpeed[guest.ID] = sample.TokensPerSecond
					guestBasis[guest.ID] = sample.RateBasis
				}
			}
		}
		var best *RPCGuest
		var bestSpeed float64
		// Require 15% headroom before claiming an RPC advantage. Small
		// network fluctuations must not trigger topology thrashing.
		if hostSpeed > 0 && size <= ^uint64(0)/115 {
			required := size * 115 / 100
			for i := range guests {
				guest := &guests[i]
				measured := guestSpeed[guest.ID]
				if measured <= hostSpeed*1.15 || measured <= bestSpeed ||
					guestBasis[guest.ID] != hostBasis || guest.CapacityBytes < required {
					continue
				}
				candidate := clusterplan.Candidate{ID: guest.ID, CapacityBytes: guest.CapacityBytes}
				if validatePlacementMemory(meta, s, size, 0, []clusterplan.Candidate{candidate}) != nil {
					continue
				}
				best, bestSpeed = guest, measured
			}
		}
		if best != nil {
			excluded := make([]string, 0, len(guests)-1)
			for _, guest := range guests {
				if guest.ID != best.ID {
					excluded = append(excluded, guest.ID)
				}
			}
			return clusterplan.Selection{
				LocalLayers: 0,
				Guests: []clusterplan.Candidate{{ID: best.ID, CapacityBytes: best.CapacityBytes}},
				Excluded: excluded,
				Reason: "Single remote Guest benchmark exceeded Host-only decode by at least 15%; no layer splitting between Host and Guest",
			}, nil
		}
		excluded := make([]string, 0, len(guests))
		for _, guest := range guests {
			excluded = append(excluded, guest.ID)
		}
		return clusterplan.Selection{
			LocalLayers: int(meta.BlockCount), Excluded: excluded,
			Reason: "Host-only memory admission passed; keep sequential decode local unless the same benchmark proves a whole-model Guest at least 15% faster. Capacity mode remains available explicitly",
		}, nil
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
	selection, err := clusterplan.Select(meta.BlockCount, size, hostBudget, candidates, strategy)
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
	if strategy != "speed" {
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

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package clusterplan

import (
	"errors"
	"math"
	"math/bits"
	"sort"
)

// Performance is observed telemetry, not a prediction of cluster throughput.
type Performance struct {
	TokensPerSecond        float64
	RoundTripMS            float64
	MiBPerSecond           float64
	NetworkMeasured        bool
	MicrobenchMiBPerSecond float64
}

type Candidate struct {
	ID            string
	CapacityBytes uint64
	Performance   Performance
}
type Selection struct {
	Guests      []Candidate
	Excluded    []string
	LocalLayers int
	Reason      string
}

// Select conservatively chooses the RPC subset BEFORE loading the model.
// Capacity mode preserves the previous all-Guests behavior.
// Speed mode minimizes participating links after choosing measured-priority
// Guests with enough aggregate RAM; real performance requires benchmarking.
func Select(blocks uint32, modelBytes, hostBudget uint64, candidates []Candidate, mode string) (Selection, error) {
	return SelectWithHostSpeed(blocks, modelBytes, hostBudget, 0, candidates, mode)
}

// SelectWithHostSpeed chooses the RPC subset and adjusts local vs remote layer
// counts using comparable throughput supplied by the caller.
func SelectWithHostSpeed(blocks uint32, modelBytes, hostBudget uint64, hostSpeed float64, candidates []Candidate, mode string) (Selection, error) {
	return selectWithHostSpeed(blocks, modelBytes, hostBudget, hostSpeed, candidates, mode, false)
}

// SelectWithHostSpeedAndRTT explicitly opts into estimated serial decode cost.
func SelectWithHostSpeedAndRTT(blocks uint32, modelBytes, hostBudget uint64, hostSpeed float64, candidates []Candidate, mode string) (Selection, error) {
	return selectWithHostSpeed(blocks, modelBytes, hostBudget, hostSpeed, candidates, mode, true)
}

func selectWithHostSpeed(blocks uint32, modelBytes, hostBudget uint64, hostSpeed float64, candidates []Candidate, mode string, estimateRTT bool) (Selection, error) {
	if mode != "" && mode != "capacity" && mode != "speed" {
		return Selection{}, errors.New("invalid placement strategy")
	}
	if len(candidates) == 0 {
		return Selection{}, errors.New("no RPC Guests available")
	}
	seen := make(map[string]bool, len(candidates))
	ranked := make([]Candidate, 0, len(candidates))
	for _, node := range candidates {
		if node.ID == "" || seen[node.ID] {
			return Selection{}, errors.New("duplicate or missing RPC Guest ID")
		}
		seen[node.ID] = true
		if node.CapacityBytes == 0 {
			return Selection{}, errors.New("RPC Guest does not have a usable memory report")
		}
		ranked = append(ranked, node)
	}
	if mode == "speed" {
		sort.SliceStable(ranked, func(i, j int) bool {
			a, b := ranked[i], ranked[j]
			linkRank := func(n Candidate) int {
				p := n.Performance
				if p.TokensPerSecond > 0 {
					// Actual inference throughput is stronger evidence than
					// a network-only heuristic, even on a slower link.
					return 4
				}
				if p.MicrobenchMiBPerSecond > 0 {
					return 3
				}
				if p.NetworkMeasured && (p.RoundTripMS > 15 || p.MiBPerSecond < 25) {
					return 0
				}
				if p.NetworkMeasured {
					return 2
				}
				return 1
			}
			if linkRank(a) != linkRank(b) {
				return linkRank(a) > linkRank(b)
			}
			if a.Performance.TokensPerSecond > 0 && b.Performance.TokensPerSecond > 0 && a.Performance.TokensPerSecond != b.Performance.TokensPerSecond {
				return a.Performance.TokensPerSecond > b.Performance.TokensPerSecond
			}
			if a.Performance.MicrobenchMiBPerSecond != b.Performance.MicrobenchMiBPerSecond {
				return a.Performance.MicrobenchMiBPerSecond > b.Performance.MicrobenchMiBPerSecond
			}
			if a.Performance.NetworkMeasured && b.Performance.NetworkMeasured {
				if a.Performance.RoundTripMS != b.Performance.RoundTripMS {
					return a.Performance.RoundTripMS < b.Performance.RoundTripMS
				}
				if a.Performance.MiBPerSecond != b.Performance.MiBPerSecond {
					return a.Performance.MiBPerSecond > b.Performance.MiBPerSecond
				}
			}
			return a.CapacityBytes > b.CapacityBytes
		})
	}
	selected := make([]Candidate, 0, len(ranked))
	budgets := make([]uint64, 0, len(ranked))
	var layers int
	var err error
	for _, guest := range ranked {
		selected = append(selected, guest)
		budgets = append(budgets, guest.CapacityBytes)
		layers, err = LocalLayerCount(blocks, modelBytes, hostBudget, budgets)
		if err == nil && mode == "speed" {
			break
		}
	}
	if err != nil {
		return Selection{}, err
	}
	speedWeighted := false
	if positiveRate(hostSpeed) && len(selected) > 0 && modelBytes <= ^uint64(0)/115 {
		guestSpeedSum := 0.0
		allMeasured := true
		var remoteBudget uint64
		for _, g := range selected {
			if !positiveRate(g.Performance.TokensPerSecond) {
				allMeasured = false
				break
			}
			guestSpeedSum += g.Performance.TokensPerSecond
			remoteBudget += g.CapacityBytes
		}
		if allMeasured && positiveRate(hostSpeed+guestSpeedSum) {
			totalSpeed := hostSpeed + guestSpeedSum
			target := int(math.Round(float64(blocks) * (hostSpeed / totalSpeed)))
			required := modelBytes * 115 / 100
			// Find memory-safe [minLocal, maxLocal] bounds within [0, blocks-1].
			maxLocal := min(int(blocks)-1, layerCapacity(blocks, required, hostBudget))
			minLocal := max(0, int(blocks)-layerCapacity(blocks, required, remoteBudget))
			if minLocal <= maxLocal {
				if target < minLocal {
					target = minLocal
				}
				if target > maxLocal {
					target = maxLocal
				}
				layers = target
				speedWeighted = true
			}
		}
	}
	// The legacy memory/speed split remains the fallback for missing RTT or
	// incompatible measurements. Capacity mode never drops Guests.
	costEstimated := false
	if mode == "speed" && estimateRTT {
		if local, cost, ok := estimatedPlacement(blocks, modelBytes, hostBudget, hostSpeed, selected); ok {
			layers, costEstimated = local, true
			best := selected
			// Compare ranked prefixes and each single Guest. This bounds
			// search cost without an exponential enumeration of subsets.
			consider := func(nodes []Candidate) {
				if next, estimate, ok := estimatedPlacement(blocks, modelBytes, hostBudget, hostSpeed, nodes); ok && estimate < cost {
					best, layers, cost = nodes, next, estimate
				}
			}
			for i := range ranked {
				consider(ranked[i : i+1])
				if i+1 > len(selected) {
					consider(ranked[:i+1])
				}
			}
			selected = best
		}
	}
	result := Selection{Guests: selected, LocalLayers: layers}
	if costEstimated {
		result.Reason = "Estimated serial decode cost (comparable benchmarks and measured RTT); not measured cluster throughput"
	} else if speedWeighted {
		result.Reason = "Speed-weighted placement from measured Host and Guest tokens/s within memory bounds"
	} else if mode == "speed" {
		result.Reason = "Memory-proportional RPC subset; comparable Host/Guest benchmarks unavailable; compare real cluster tokens/s before claiming acceleration"
	} else {
		result.Reason = "All connected RPC Guests; placement proportional to available memory"
	}
	chosen := make(map[string]bool, len(selected))
	for _, n := range selected {
		chosen[n.ID] = true
	}
	for _, n := range ranked {
		if !chosen[n.ID] {
			result.Excluded = append(result.Excluded, n.ID)
		}
	}
	return result, nil
}

func positiveRate(rate float64) bool {
	return rate > 0 && !math.IsInf(rate, 0) && !math.IsNaN(rate)
}

// layerCapacity uses a full-width product to avoid overflow in memory clamps.
func layerCapacity(blocks uint32, required, budget uint64) int {
	if budget >= required {
		return int(blocks)
	}
	hi, lo := bits.Mul64(uint64(blocks), budget)
	layers, _ := bits.Div64(hi, lo, required)
	return int(layers)
}

// estimatedPlacement models serial decode, not observed throughput. Remote
// layers follow the existing memory-proportional device split; each used
// remote segment adds two RTTs per token as a conservative heuristic.
func estimatedPlacement(blocks uint32, modelBytes, hostBudget uint64, hostSpeed float64, guests []Candidate) (int, float64, bool) {
	if blocks == 0 || blocks > 4096 || !positiveRate(hostSpeed) || len(guests) == 0 || len(guests) > int(blocks) {
		return 0, 0, false
	}
	budgets := make([]uint64, 0, len(guests))
	var remote uint64
	for _, g := range guests {
		p := g.Performance
		if !positiveRate(p.TokensPerSecond) || !p.NetworkMeasured || p.RoundTripMS < 0 ||
			math.IsNaN(p.RoundTripMS) || math.IsInf(p.RoundTripMS, 0) ||
			g.CapacityBytes > ^uint64(0)-remote {
			return 0, 0, false
		}
		budgets = append(budgets, g.CapacityBytes)
		remote += g.CapacityBytes
	}
	if _, err := LocalLayerCount(blocks, modelBytes, hostBudget, budgets); err != nil {
		return 0, 0, false
	}
	required := modelBytes * 115 / 100
	minLocal := max(0, int(blocks)-layerCapacity(blocks, required, remote))
	maxLocal := min(int(blocks)-len(guests), layerCapacity(blocks, required, hostBudget))
	bestLayers, bestCost := 0, math.Inf(1)
	for local := minLocal; local <= maxLocal; local++ {
		remaining, remainingBudget := int(blocks)-local, remote
		cost := float64(local) / float64(blocks) / hostSpeed
		for i, guest := range guests {
			layers := remaining
			if i < len(guests)-1 {
				hi, lo := bits.Mul64(uint64(remaining), guest.CapacityBytes)
				share, _ := bits.Div64(hi, lo, remainingBudget)
				layers = int(share)
			}
			if layers == 0 || layers > layerCapacity(blocks, required, guest.CapacityBytes) {
				cost = math.Inf(1)
				break
			}
			remaining -= layers
			remainingBudget -= guest.CapacityBytes
			cost += float64(layers)/float64(blocks)/guest.Performance.TokensPerSecond + 2*guest.Performance.RoundTripMS/1000
		}
		if cost < bestCost {
			bestLayers, bestCost = local, cost
		}
	}
	return bestLayers, bestCost, positiveRate(bestCost)
}

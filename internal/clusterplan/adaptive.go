// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package clusterplan

import (
	"errors"
	"math"
	"sort"
)

// Performance is observed telemetry, not a prediction of cluster throughput.
type Performance struct {
	TokensPerSecond float64
	RoundTripMS     float64
	MiBPerSecond    float64
	NetworkMeasured bool
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
// counts proportionally to measured tokens/s when both Host and all selected
// Guests have valid throughput benchmarks, bounded by memory admission.
func SelectWithHostSpeed(blocks uint32, modelBytes, hostBudget uint64, hostSpeed float64, candidates []Candidate, mode string) (Selection, error) {
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
	if hostSpeed > 0 && len(selected) > 0 && modelBytes <= ^uint64(0)/115 {
		guestSpeedSum := 0.0
		allMeasured := true
		var remoteBudget uint64
		for _, g := range selected {
			if g.Performance.TokensPerSecond <= 0 {
				allMeasured = false
				break
			}
			guestSpeedSum += g.Performance.TokensPerSecond
			remoteBudget += g.CapacityBytes
		}
		if allMeasured {
			totalSpeed := hostSpeed + guestSpeedSum
			target := int(math.Round(float64(blocks) * hostSpeed / totalSpeed))
			required := modelBytes * 115 / 100
			// Find memory-safe [minLocal, maxLocal] bounds within [0, blocks-1].
			maxLocal := int(blocks) - 1
			for maxLocal > 0 && required*uint64(maxLocal)/uint64(blocks) > hostBudget {
				maxLocal--
			}
			minLocal := 0
			for minLocal < int(blocks)-1 && required*uint64(int(blocks)-minLocal)/uint64(blocks) > remoteBudget {
				minLocal++
			}
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
	result := Selection{Guests: selected, LocalLayers: layers}
	if speedWeighted {
		result.Reason = "Speed-weighted placement from measured Host and Guest tokens/s within memory bounds"
	} else if mode == "speed" {
		result.Reason = "Measured-priority RPC subset with enough estimated memory; compare real cluster tokens/s before claiming acceleration"
	} else {
		result.Reason = "All connected RPC Guests; placement proportional to available memory"
	}
	for _, n := range ranked[len(selected):] {
		result.Excluded = append(result.Excluded, n.ID)
	}
	return result, nil
}

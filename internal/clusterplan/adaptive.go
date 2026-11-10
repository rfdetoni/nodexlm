// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package clusterplan

import (
	"errors"
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
	result := Selection{Guests: selected, LocalLayers: layers}
	if mode == "speed" {
		result.Reason = "Measured-priority RPC subset with enough estimated memory; compare real cluster tokens/s before claiming acceleration"
	} else {
		result.Reason = "All connected RPC Guests; placement proportional to available memory"
	}
	for _, n := range ranked[len(selected):] {
		result.Excluded = append(result.Excluded, n.ID)
	}
	return result, nil
}

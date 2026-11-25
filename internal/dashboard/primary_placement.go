// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"errors"
	"fmt"
	"sort"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
)

func explicitPrimary(id string) bool { return id != "" && id != "auto" }

// choosePrimary optimizes contiguous assignments in deterministic Guest order.
// A suffix feasibility table avoids exponential subset/permutation search.
func choosePrimary(s Settings, meta local.GGUFInfo, size, host uint64, available []RPCGuest) (clusterplan.Selection, error) {
	fail := func(msg string) (clusterplan.Selection, error) { return clusterplan.Selection{}, errors.New(msg) }
	n := int(meta.BlockCount)
	if n < 1 || n > 4096 || size == 0 || size > ^uint64(0)/8192 || len(available) > 128 {
		return fail("invalid primary placement size or topology")
	}
	if s.GPULayers != "auto" && s.GPULayers != "all" && s.GPULayers != "" {
		return fail("nó principal explícito exige camadas GPU auto/all para preservar o split")
	}
	hostPrimary := s.PrimaryNode == "host"
	ordered := make([]RPCGuest, 0, len(available))
	seen := map[string]bool{}
	found, legacy := hostPrimary, false
	for _, g := range available {
		if g.ID == "" || seen[g.ID] {
			return fail("duplicate or missing RPC Guest ID")
		}
		seen[g.ID] = true
		if g.CapacityBytes == 0 {
			continue
		}
		found = found || g.ID == s.PrimaryNode
		legacy = legacy || !validRPCDeviceKinds(g)
		ordered = append(ordered, g)
	}
	if !found {
		return fail("nó principal indisponível/offline ou sem orçamento de memória")
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.ID == s.PrimaryNode {
			return true
		}
		if b.ID == s.PrimaryNode {
			return false
		}
		return a.ID < b.ID
	})
	candidates := make([]clusterplan.Candidate, len(ordered))
	for i, g := range ordered {
		candidates[i] = clusterplan.Candidate{ID: g.ID, CapacityBytes: g.CapacityBytes}
	}
	if legacy {
		result, err := clusterplan.Select(meta.BlockCount, size, host, candidates, "capacity")
		if err == nil {
			err = validatePlacementMemory(meta, s, size, host, result.Guests)
		}
		result.Reason = "Guest legacy sem topologia nativa: preferência principal não pode ser garantida; fallback proporcional à memória"
		return result, err
	}
	if err := validatePlacementMemory(meta, s, size, host, candidates); err != nil {
		return clusterplan.Selection{}, err
	}
	cache := uint64(s.PromptCacheMiB) * clusterplan.MiB
	if cache > host {
		return fail("cache de prompts excede orçamento do Host")
	}
	host -= cache
	prefix := make([]uint64, n+1)
	for i := 0; i < n; i++ {
		w := estimatedBlockWeight(meta, size, i, 1)
		if w == 0 || w > ^uint64(0)/115-prefix[i] {
			return fail("invalid GGUF block weights")
		}
		prefix[i+1] = prefix[i] + w
	}
	nonBlock := meta.NonBlockBytes
	if len(meta.BlockBytes) != n {
		// Unknown pinned output/embedding weight: keep one block's estimated
		// weight as extra Host reserve, rather than claim zero Host RAM.
		nonBlock = size/uint64(n) + 1
	}
	if nonBlock > ^uint64(0)/115-prefix[n] {
		return fail("invalid non-block weights")
	}
	transient := estimateTransientBytes(meta, s)
	fits := func(start, end int, extra, budget uint64) bool {
		weight := prefix[end] - prefix[start] + extra
		required := weight * 115 / 100
		if transient != nil {
			count := uint64(end - start)
			if count > 0 && *transient > ^uint64(0)/count {
				return false
			}
			contextBytes := *transient * count / uint64(n)
			if contextBytes > ^uint64(0)-required {
				return false
			}
			required += contextBytes
		}
		return required <= budget
	}
	maxEnd := func(start int, budget uint64) int {
		return start + sort.Search(n-start, func(i int) bool { return !fits(start, start+i+1, 0, budget) })
	}
	guestEnd := func(g RPCGuest, start int) int {
		end := start
		if g.RPCDeviceKinds[0] == "gpu" {
			end = maxEnd(start, g.GPUBudgetBytes)
			if len(g.RPCDeviceKinds) == 2 {
				end = maxEnd(end, g.RAMBudgetBytes)
			}
		} else {
			end = maxEnd(start, g.RAMBudgetBytes)
		}
		end = min(end, maxEnd(start, g.CapacityBytes))
		return end
	}
	// Each table entry stores the farthest feasible end given the remaining
	// Guests. In speed mode secondary Guests may receive zero layers.
	capacity := effectivePlacementStrategy(s.PlacementStrategy) == "capacity"
	choices := make([][]int, len(ordered))
	feasible := make([]bool, n+1)
	feasible[n] = true
	for i := len(ordered) - 1; i >= 0; i-- {
		last := make([]int, n+1)
		prev := -1
		for j, ok := range feasible {
			if ok {
				prev = j
			}
			last[j] = prev
		}
		row := make([]int, n+1)
		next := make([]bool, n+1)
		minLayers := 0
		if capacity || ordered[i].ID == s.PrimaryNode {
			minLayers = 1
		}
		for start := 0; start <= n; start++ {
			end := last[guestEnd(ordered[i], start)]
			row[start] = -1
			if end >= start+minLayers {
				row[start], next[start] = end, true
			}
		}
		choices[i], feasible = row, next
	}
	bestLocal := -1
	var bestWeight uint64
	for h := 0; h <= n; h++ {
		if !fits(0, h, nonBlock, host) {
			break
		}
		if !feasible[h] {
			continue
		}
		weight := prefix[h]
		if !hostPrimary {
			weight = prefix[choices[0][h]] - prefix[h]
		}
		if bestLocal < 0 || weight > bestWeight {
			bestLocal, bestWeight = h, weight
		}
	}
	if bestLocal < 0 {
		return fail("nenhuma divisão contígua segura para o nó principal e budgets atuais")
	}
	result := clusterplan.Selection{LocalLayers: bestLocal, Reason: "principal explícito: fatia maximizada na ordem de nós selecionada; mais pesos ≠ mais velocidade (RPC sequencial); compare com A/B"}
	start := bestLocal
	for i := range ordered {
		end := choices[i][start]
		if end > start {
			result.Guests = append(result.Guests, candidates[i])
			result.GuestLayers = append(result.GuestLayers, end-start)
		}
		start = end
	}
	for _, g := range available {
		included := false
		for _, c := range result.Guests {
			included = included || c.ID == g.ID
		}
		if !included {
			result.Excluded = append(result.Excluded, g.ID)
		}
	}
	if err := validatePlacementMemory(meta, s, size, host+cache, result.Guests); err != nil {
		return clusterplan.Selection{}, err
	}
	if len(result.Guests) > 0 && len(planRPCDevices(result, available, meta, s, size).Devices) == 0 {
		return fail(fmt.Sprintf("split do nó principal %q excede orçamento de dispositivo", s.PrimaryNode))
	}
	return result, nil
}

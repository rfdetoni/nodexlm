// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
)

// benchmarkPlacement freezes the baseline's native arguments, not its memory
// admission. Each candidate rechecks current node budgets and its own context.
type benchmarkPlacement struct {
	ready       bool
	selection   clusterplan.Selection
	devices     RPCDevicePlacement
	localLayers int
	guests      []RPCGuest
}

// waitForRPCIdle gives a Guest's authenticated relay a bounded opportunity
// to replenish its idle tunnels after the prior benchmark round releases native
// connections. A different endpoint or native device kinds is genuine topology
// drift and must still fail immediately. A missing Guest never silently causes
// the benchmark to replan to a different placement.
func (p *benchmarkPlacement) waitForRPCIdle(ctx context.Context, provider func() []RPCGuest) error {
	if provider == nil {
		return errors.New("fixed cluster RPC provider unavailable")
	}
	original := make(map[string]RPCGuest, len(p.guests))
	for _, guest := range p.guests {
		original[guest.ID] = guest
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		available := make(map[string]RPCGuest)
		for _, guest := range provider() {
			available[guest.ID] = guest
		}
		missing := false
		for _, chosen := range p.selection.Guests {
			prev, originallySelected := original[chosen.ID]
			if !originallySelected {
				return errors.New("fixed cluster Guest topology changed")
			}
			current, ok := available[chosen.ID]
			if !ok {
				missing = true
				continue
			}
			if current.Endpoint != prev.Endpoint || !validRPCDeviceKinds(current) ||
				!slices.Equal(current.RPCDeviceKinds, prev.RPCDeviceKinds) {
				return errors.New("fixed cluster Guest topology changed")
			}
		}
		if !missing {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("fixed cluster Guest idle RPC tunnels did not recover")
		case <-ticker.C:
		}
	}
}

func (p *benchmarkPlacement) validate(guests []RPCGuest, meta local.GGUFInfo, s Settings, size, hostBudget uint64) error {
	if len(p.devices.Devices) == 0 || len(p.devices.Devices) != len(p.devices.Split) {
		return errors.New("fixed cluster autotune requires explicit native device telemetry; legacy inference remains supported")
	}
	byID := make(map[string]RPCGuest, len(guests))
	for _, guest := range guests {
		byID[guest.ID] = guest
	}
	original := make(map[string]RPCGuest, len(p.guests))
	for _, guest := range p.guests {
		original[guest.ID] = guest
	}
	budgets := make(map[string]uint64)
	selected := make([]clusterplan.Candidate, 0, len(p.selection.Guests))
	rpcIndex := 0
	for _, chosen := range p.selection.Guests {
		guest, ok := byID[chosen.ID]
		if !ok || guest.Endpoint != original[chosen.ID].Endpoint || !validRPCDeviceKinds(guest) || !slices.Equal(guest.RPCDeviceKinds, original[chosen.ID].RPCDeviceKinds) {
			return errors.New("fixed cluster Guest topology changed")
		}
		selected = append(selected, clusterplan.Candidate{ID: guest.ID, CapacityBytes: guest.CapacityBytes})
		for _, kind := range guest.RPCDeviceKinds {
			budget := rpcDeviceBudget(guest, kind)
			budgets[fmt.Sprintf("RPC%d", rpcIndex)] = budget
			rpcIndex++
		}
	}
	if err := validatePlacementMemory(meta, s, size, hostBudget, selected); err != nil {
		return err
	}
	blocks := int(meta.BlockCount)
	if blocks <= 0 || blocks > 4096 || p.localLayers < 0 || p.localLayers >= blocks || size > ^uint64(0)/uint64(max(115, blocks)) {
		return errors.New("invalid fixed cluster layer budget")
	}
	transient := estimateTransientBytes(meta, s)
	// Native split values are proportions. Use cumulative rounded-up layer
	// boundaries and the same conservative per-layer weight/KV admission.
	var splitSum uint64
	for _, split := range p.devices.Split {
		if split == 0 || split > ^uint64(0)-splitSum {
			return errors.New("invalid fixed tensor split")
		}
		splitSum += split
	}
	if splitSum > ^uint64(0)/uint64(blocks+1) {
		return errors.New("fixed tensor split overflow")
	}
	need := func(start, count int) uint64 {
		weight := size * uint64(count) / uint64(blocks)
		if len(meta.BlockBytes) == blocks {
			weight = blockWeight(meta, start, count)
		}
		if weight > ^uint64(0)/115 {
			return ^uint64(0)
		}
		weight = weight * 115 / 100
		if transient != nil {
			if *transient > ^uint64(0)/uint64(blocks) {
				return ^uint64(0)
			}
			reserve := *transient * uint64(count) / uint64(blocks)
			if reserve > ^uint64(0)-weight {
				return ^uint64(0)
			}
			weight += reserve
		}
		return weight
	}
	hostNeed := need(0, p.localLayers)
	if len(meta.BlockBytes) == blocks {
		if meta.NonBlockBytes > ^uint64(0)/115 || meta.NonBlockBytes*115/100 > ^uint64(0)-hostNeed {
			return errors.New("fixed Host memory overflow")
		}
		hostNeed += meta.NonBlockBytes * 115 / 100
	}
	if s.RPCNativeScheduler && (len(meta.BlockBytes) != blocks || meta.NonBlockBytes == 0 || meta.NonBlockBytes > ^uint64(0)/115) {
		return errors.New("native scheduler requires known non-block tensor sizes")
	}
	var cumulative uint64
	start := p.localLayers
	for i, device := range p.devices.Devices {
		cumulative += p.devices.Split[i]
		end := p.localLayers + int((cumulative*uint64(blocks-p.localLayers)+splitSum-1)/splitSum)
		required := need(start, end-start)
		if s.RPCNativeScheduler && i == len(p.devices.Devices)-1 {
			extra := meta.NonBlockBytes * 115 / 100
			if extra > ^uint64(0)-required {
				return errors.New("native output memory overflow")
			}
			required += extra
		}
		if budget, ok := budgets[device]; ok {
			if required > budget {
				return fmt.Errorf("fixed device %s exceeds current estimated memory budget", device)
			}
		} else if i == 0 && p.devices.Modes["host"] != "" {
			if required > ^uint64(0)-hostNeed {
				return errors.New("fixed Host memory overflow")
			}
			hostNeed += required
		} else {
			return errors.New("fixed native device is no longer available")
		}
		start = end
	}
	if hostNeed > hostBudget {
		return errors.New("fixed Host placement exceeds current estimated memory budget")
	}
	return nil
}

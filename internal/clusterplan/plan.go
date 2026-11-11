// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package clusterplan

import (
	"errors"
	"sort"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

const MiB uint64 = 1 << 20
const GiB uint64 = 1 << 30

type NodeCapacity struct {
	ID                  string                     `json:"id"`
	Name                string                     `json:"name"`
	Platform            string                     `json:"platform"`
	CPUCount            int                        `json:"cpu_count"`
	RAMBytes            uint64                     `json:"ram_bytes"`
	AvailableRAMBytes   uint64                     `json:"available_ram_bytes"`
	FreeVRAMBytes       uint64                     `json:"free_vram_bytes"`
	ReservedUsableBytes uint64                     `json:"estimated_model_budget_bytes"`
	MaxContributionBytes uint64                   `json:"max_contribution_bytes,omitempty"`
	MemoryReserveBytes uint64                     `json:"memory_reserve_bytes,omitempty"`
	Runtime             protocol.RuntimeCapability `json:"runtime"`
	Status              string                     `json:"status"`
}
type Summary struct {
	Nodes                  []NodeCapacity `json:"nodes"`
	Online                 int            `json:"online"`
	InferenceReady         int            `json:"inference_ready"`
	AggregateBudgetBytes   uint64         `json:"aggregate_budget_bytes"`
	LargestNodeBudgetBytes uint64         `json:"largest_node_budget_bytes"`
	Disclaimer             string         `json:"disclaimer"`
}

func Budget(d protocol.Device) uint64 {
	// This is one whole-node budget, never RAM + VRAM. Shared GPU heaps
	// cannot increase the physical RAM actually available to the process.
	avail := d.AvailableRAMBytes
	if d.RAMBytes > 0 && avail > d.RAMBytes {
		avail = d.RAMBytes
	}
	for _, g := range d.GPUs {
		free := g.FreeVRAMBytes
		switch {
		case d.Platform == "android" || g.MemoryType == "shared":
			// A legacy Android GPU report is shared even if not annotated.
			// Unknown system availability is not unlimited shared VRAM.
			if free > d.AvailableRAMBytes {
				free = d.AvailableRAMBytes
			}
		case g.MemoryType != "dedicated":
			// Unknown memory type is not proof of separate VRAM.
			if d.AvailableRAMBytes > 0 && free > d.AvailableRAMBytes {
				free = d.AvailableRAMBytes
			}
			if d.RAMBytes > 0 && free > d.RAMBytes {
				free = d.RAMBytes
			}
		}
		if free > avail {
			avail = free
		}
	}
	reserve := GiB
	if d.MemoryReserveBytes > reserve {
		reserve = d.MemoryReserveBytes
	}
	if avail <= reserve {
		return 0
	}
	budget := (avail - reserve) * 80 / 100
	if d.MaxContributionBytes > 0 && budget > d.MaxContributionBytes {
		budget = d.MaxContributionBytes
	}
	return budget
}
func Aggregate(devices []protocol.Device, now time.Time) Summary {
	out := Summary{Nodes: make([]NodeCapacity, 0, len(devices)), Disclaimer: "Memory estimates are conservative and do not guarantee model compatibility, RPC network throughput, or KV cache fit. Single-model RPC execution is experimental; compatibility and network performance must be validated."}
	for _, d := range devices {
		online := d.Status == "online" && !d.LastSeen.IsZero() && now.Sub(d.LastSeen) <= 65*time.Second
		status := "offline"
		if online {
			status = "online"
			out.Online++
		}
		budget := uint64(0)
		if online {
			budget = Budget(d)
			out.AggregateBudgetBytes += budget
			if budget > out.LargestNodeBudgetBytes {
				out.LargestNodeBudgetBytes = budget
			}
		}
		freeVRAM := uint64(0)
		for _, g := range d.GPUs {
			// Multiple shared heaps describe the same physical memory.
			if d.Platform == "android" || g.MemoryType == "shared" {
				if g.FreeVRAMBytes > freeVRAM {
					freeVRAM = g.FreeVRAMBytes
				}
			} else if freeVRAM <= ^uint64(0)-g.FreeVRAMBytes {
				freeVRAM += g.FreeVRAMBytes
			}
		}
		if online && d.Runtime.Installed {
			out.InferenceReady++
		}
		out.Nodes = append(out.Nodes, NodeCapacity{ID: d.ID, Name: d.Name, Platform: d.Platform, CPUCount: d.CPUCount, RAMBytes: d.RAMBytes, AvailableRAMBytes: d.AvailableRAMBytes, FreeVRAMBytes: freeVRAM, ReservedUsableBytes: budget, MaxContributionBytes: d.MaxContributionBytes, MemoryReserveBytes: d.MemoryReserveBytes, Runtime: d.Runtime, Status: status})
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].Name < out.Nodes[j].Name })
	return out
}

type ModelCandidate struct {
	Repository string `json:"repository"`
	Filename   string `json:"filename"`
	SizeBytes  uint64 `json:"size_bytes"`
	URL        string `json:"url"`
	ShardCount int    `json:"shard_count,omitempty"`
}
type Recommendation struct {
	ModelCandidate
	Placement string `json:"placement"`
	Reason    string `json:"reason"`
}

func Assess(s Summary, m ModelCandidate) Recommendation {
	result := Recommendation{ModelCandidate: m, Placement: "unknown", Reason: "No reliable available-memory report from online nodes"}
	if m.SizeBytes == 0 {
		return result
	}
	// GGUF size is not the full memory footprint; leave headroom beyond model bytes.
	if m.SizeBytes > ^uint64(0)/115 {
		result.Reason = "Model size metadata exceeds supported range"
		return result
	}
	required := m.SizeBytes * 115 / 100
	if s.LargestNodeBudgetBytes >= required {
		result.Placement = "single-node-candidate"
		result.Reason = "Estimated to fit on one reported node, subject to GGUF architecture, backend and context validation"
	} else if s.AggregateBudgetBytes >= required && s.Online >= 2 {
		result.Placement = "distributed-candidate"
		result.Reason = "Estimated aggregate memory may support layer-distributed RPC inference if nodes have compatible active RPC backends"
	} else if s.AggregateBudgetBytes > 0 {
		result.Placement = "insufficient-memory"
		result.Reason = "Estimated free model budget is below GGUF weights plus headroom"
	}
	return result
}

// LocalLayerCount mirrors the PrivateLM layer planner: first transformer
// blocks remain on the host CPU, remaining blocks are offloaded to the RPC
// backends. Model bytes are an estimate, not a guarantee of allocation.
// The output embedding is separately pinned to the host by the runtime.
func LocalLayerCount(blocks uint32, modelBytes, hostBudget uint64, guests []uint64) (int, error) {
	if blocks == 0 || modelBytes == 0 || len(guests) == 0 {
		return 0, errors.New("distributed layer placement requires GGUF blocks, size and RPC Guests")
	}
	remote := uint64(0)
	for _, capacity := range guests {
		if capacity == 0 || remote > ^uint64(0)-capacity {
			return 0, errors.New("invalid RPC Guest memory budget")
		}
		remote += capacity
	}
	if hostBudget > ^uint64(0)-remote {
		return 0, errors.New("cluster memory total overflow")
	}
	total := hostBudget + remote
	// Add headroom for runtime buffers and KV cache, without integer overflow.
	if modelBytes > ^uint64(0)/115 || modelBytes*115/100 > total {
		return 0, errors.New("not enough reported cluster memory for GGUF weights and context headroom")
	}
	// Local share is proportional to measured free memory, rounded to nearest
	// block. A Host with unknown capacity keeps no transformer blocks locally.
	if uint64(blocks) > ^uint64(0)/max(hostBudget, uint64(1)) {
		return 0, errors.New("layer calculation overflow")
	}
	product := uint64(blocks) * hostBudget
	local := int(product / total)
	if product%total >= total/2+total%2 {
		local++
	}
	// A distributed configuration must actually place a transformer block
	// remotely; otherwise the Guest does no useful model computation.
	if local >= int(blocks) {
		local = int(blocks) - 1
	}
	return local, nil
}

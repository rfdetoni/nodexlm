// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package clusterplan

import (
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
	// Conservative resident weight allowance. Reserve 1 GiB for each OS plus
	// another 20% of the remaining memory for KV cache and working buffers.
	avail := d.AvailableRAMBytes
	for _, g := range d.GPUs {
		// Do not add VRAM to RAM; iGPUs can share physical memory.
		if g.FreeVRAMBytes > avail {
			avail = g.FreeVRAMBytes
		}
	}
	if avail <= GiB {
		return 0
	}
	return (avail - GiB) * 80 / 100
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
			freeVRAM += g.FreeVRAMBytes
		}
		if online && d.Runtime.Installed {
			out.InferenceReady++
		}
		out.Nodes = append(out.Nodes, NodeCapacity{ID: d.ID, Name: d.Name, Platform: d.Platform, CPUCount: d.CPUCount, RAMBytes: d.RAMBytes, AvailableRAMBytes: d.AvailableRAMBytes, FreeVRAMBytes: freeVRAM, ReservedUsableBytes: budget, Runtime: d.Runtime, Status: status})
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].Name < out.Nodes[j].Name })
	return out
}

type ModelCandidate struct {
	Repository string `json:"repository"`
	Filename   string `json:"filename"`
	SizeBytes  uint64 `json:"size_bytes"`
	URL        string `json:"url"`
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

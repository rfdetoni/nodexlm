// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package clusterplan

import (
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestLocalLayerPlacement(t *testing.T) {
	const gib = uint64(1 << 30)
	for _, tt := range []struct {
		name        string
		blocks      uint32
		bytes, host uint64
		guest       []uint64
		want        int
		fail        bool
	}{
		{"balanced", 40, 7 * gib, 8 * gib, []uint64{8 * gib}, 20, false},
		{"guest_only", 40, 5 * gib, 0, []uint64{8 * gib}, 0, false},
		{"host_dominant_still_uses_guest", 8, gib, 30 * gib, []uint64{gib}, 7, false},
		{"insufficient", 32, 8 * gib, 0, []uint64{2 * gib}, 0, true},
		{"unmeasured_guest", 8, gib, 8 * gib, []uint64{0}, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LocalLayerCount(tt.blocks, tt.bytes, tt.host, tt.guest)
			if (err != nil) != tt.fail || !tt.fail && got != tt.want {
				t.Fatalf("got=%d err=%v; want=%d fail=%v", got, err, tt.want, tt.fail)
			}
		})
	}
}

func TestBudgetHonorsMobileSharedMemoryAndUserLimits(t *testing.T) {
	shared := protocol.Device{
		Platform: "android", RAMBytes: 8 * GiB, AvailableRAMBytes: 2 * GiB,
		GPUs: []protocol.GPUDevice{{MemoryType: "shared", VRAMBytes: 6 * GiB, FreeVRAMBytes: 5 * GiB}},
	}
	const want = 4 * GiB / 5 // 80% after reserving 1 GiB
	if got := Budget(shared); got != want {
		t.Fatalf("shared GPU inflated physical RAM: got %d, want %d", got, want)
	}
	shared.GPUs[0].MemoryType = "" // Older Android clients are still shared.
	if got := Budget(shared); got != want {
		t.Fatalf("legacy mobile GPU overcommit: %d", got)
	}
	shared.MaxContributionBytes = 512 * MiB
	if got := Budget(shared); got != 512*MiB {
		t.Fatalf("user memory cap ignored: %d", got)
	}
	shared.MemoryReserveBytes = 2 * GiB
	if got := Budget(shared); got != 0 {
		t.Fatalf("OS reserve ignored: %d", got)
	}
	shared.MemoryReserveBytes = 0
	shared.MaxContributionBytes = 0
	shared.AvailableRAMBytes = 0 // Never infer shared VRAM from absent physical data.
	if got := Budget(shared); got != 0 {
		t.Fatalf("unknown physical availability accepted: %d", got)
	}
}

func TestBudgetDedicatedGPUAndUnknownLegacy(t *testing.T) {
	d := protocol.Device{Platform: "linux", RAMBytes: 8 * GiB, AvailableRAMBytes: 2 * GiB,
		GPUs: []protocol.GPUDevice{{VRAMBytes: 6 * GiB, FreeVRAMBytes: 5 * GiB, MemoryType: "dedicated"}}}
	if got := Budget(d); got != 16*GiB/5 {
		t.Fatalf("dedicated GPU not recognized: %d", got)
	}
	d.GPUs[0].MemoryType = ""
	if got := Budget(d); got != 4*GiB/5 {
		t.Fatalf("unknown GPU memory was assumed dedicated: %d", got)
	}
}

func TestAggregateDoesNotSumSharedGPUHeaps(t *testing.T) {
	d := protocol.Device{ID: "android", Name: "phone", Platform: "android", RAMBytes: 8 * GiB,
		AvailableRAMBytes: 2 * GiB, LastSeen: time.Now(), Status: "online",
		GPUs: []protocol.GPUDevice{
			{MemoryType: "shared", FreeVRAMBytes: 4 * GiB},
			{MemoryType: "shared", FreeVRAMBytes: 3 * GiB},
		}}
	summary := Aggregate([]protocol.Device{d}, time.Now())
	if summary.AggregateBudgetBytes != Budget(d) || summary.Nodes[0].FreeVRAMBytes != 4*GiB {
		t.Fatalf("shared memory double counted: %+v", summary)
	}
}

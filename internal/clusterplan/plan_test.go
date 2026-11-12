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
	d.AvailableRAMBytes = 0
	if got := Budget(d); got != 0 {
		t.Fatalf("unknown physical available memory authorized unknown GPU heap: %d", got)
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

func TestAggregateDrainingExcludesMemoryBudget(t *testing.T) {
	device := protocol.Device{
		ID: "phone", Name: "phone", Platform: "android", CPUCount: 8,
		RAMBytes: 8 * GiB, AvailableRAMBytes: 4 * GiB,
		Status: "draining", UnavailableReason: "thermal", LastSeen: time.Now(),
		Runtime: protocol.RuntimeCapability{Installed: true, RPCReady: true},
	}
	result := Aggregate([]protocol.Device{device}, time.Now())
	if result.Online != 0 || result.InferenceReady != 0 || result.AggregateBudgetBytes != 0 ||
		len(result.Nodes) != 1 || result.Nodes[0].Status != "draining" ||
		result.Nodes[0].ReservedUsableBytes != 0 {
		t.Fatalf("draining node was admitted to planner: %+v", result)
	}
}

func TestDedicatedGPUHybridBudgetAddsOnlySeparateMemory(t *testing.T) {
	d := protocol.Device{
		Platform: "linux", RAMBytes: 28 * GiB, AvailableRAMBytes: 20 * GiB,
		GPUs: []protocol.GPUDevice{{MemoryType: "dedicated", VRAMBytes: 6 * GiB, FreeVRAMBytes: 5 * GiB}},
		Runtime: protocol.RuntimeCapability{RPCDevices: []string{"gpu", "cpu"}},
	}
	memory := RPCMemory(d)
	wantRAM := (20*GiB - GiB) * 80 / 100
	wantGPU := (5*GiB - 256*MiB) * 90 / 100
	if memory.RAM != wantRAM || memory.GPU != wantGPU {
		t.Fatalf("unexpected independent device capacities: got %+v want RAM=%d GPU=%d", memory, wantRAM, wantGPU)
	}
	if Budget(d) != memory.RAM+memory.GPU {
		t.Fatalf("hybrid worker cannot combine separate RAM and dedicated VRAM: %d", Budget(d))
	}
	d.Runtime.RPCDevices = []string{"gpu"}
	if Budget(d) != memory.GPU || RPCMemory(d).RAM != 0 {
		t.Fatal("GPU-only worker incorrectly advertises RAM capacity")
	}
	d.Runtime.RPCDevices = []string{"cpu"}
	if Budget(d) != memory.RAM || RPCMemory(d).GPU != 0 {
		t.Fatal("CPU-only worker incorrectly advertises VRAM capacity")
	}
	d.MaxContributionBytes = 3 * GiB
	if Budget(d) != 3*GiB {
		t.Fatal("max contribution limit not applied to hybrid workers")
	}
}

func TestSharedGPUHybridNeverDoubleCountsRAM(t *testing.T) {
	d := protocol.Device{Platform: "android", RAMBytes: 8 * GiB, AvailableRAMBytes: 5 * GiB,
		GPUs: []protocol.GPUDevice{{MemoryType: "shared", VRAMBytes: 8 * GiB, FreeVRAMBytes: 5 * GiB}},
		Runtime: protocol.RuntimeCapability{RPCDevices: []string{"gpu", "cpu"}},
	}
	memory := RPCMemory(d)
	if memory.GPU != 0 || memory.RAM == 0 || Budget(d) != memory.RAM {
		t.Fatalf("shared VRAM was double-counted: %+v total %d", memory, Budget(d))
	}
	d.Platform = "linux"
	d.GPUs[0].MemoryType = "" // Unknown memory class cannot be assumed discrete.
	if RPCMemory(d).GPU != 0 || Budget(d) != memory.RAM {
		t.Fatal("unknown GPU memory was added as dedicated VRAM")
	}
}

func TestHybridBudgetDoesNotSumUnmappedMultipleGPUs(t *testing.T) {
	d := protocol.Device{Platform: "linux", RAMBytes: 32 * GiB, AvailableRAMBytes: 12 * GiB,
		GPUs: []protocol.GPUDevice{
			{MemoryType: "dedicated", FreeVRAMBytes: 6 * GiB},
			{MemoryType: "dedicated", FreeVRAMBytes: 6 * GiB},
		},
		Runtime: protocol.RuntimeCapability{RPCDevices: []string{"gpu", "cpu"}},
	}
	if RPCMemory(d).GPU >= 6*GiB {
		t.Fatal("unmapped GPU cards were pooled into one native device's VRAM")
	}
}

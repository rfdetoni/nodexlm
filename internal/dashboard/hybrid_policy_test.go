package dashboard

import (
	"strings"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
)

func TestRPCPlacementPrefersOnlyGPUWhenWeightsFitVRAM(t *testing.T) {
	const gib = uint64(1 << 30)
	guest := RPCGuest{ID: "node", CapacityBytes: 19 * gib,
		RPCDeviceKinds: []string{"gpu", "cpu"},
		GPUBudgetBytes: 6 * gib, RAMBudgetBytes: 13 * gib}
	selection := clusterplan.Selection{LocalLayers: 0,
		Guests: []clusterplan.Candidate{{ID: "node", CapacityBytes: guest.CapacityBytes}}}
	s := Settings{Model: "model.gguf", ContextTokens: 2048}
	plan := planRPCDevices(selection, []RPCGuest{guest}, local.GGUFInfo{BlockCount: 42}, s, 4*gib)
	if plan.DeviceArg() != "RPC0" || plan.TensorSplitArg() != "" ||
		plan.Modes["node"] != "gpu" {
		t.Fatalf("GPU-only model unexpectedly uses CPU: %+v", plan)
	}
}

func TestRPCPlacementUsesRAMOnlyWhenVRAMTooSmall(t *testing.T) {
	const gib = uint64(1 << 30)
	guest := RPCGuest{ID: "node", CapacityBytes: 18 * gib,
		RPCDeviceKinds: []string{"gpu", "cpu"},
		GPUBudgetBytes: 3 * gib, RAMBudgetBytes: 15 * gib}
	selection := clusterplan.Selection{LocalLayers: 0,
		Guests: []clusterplan.Candidate{{ID: "node", CapacityBytes: guest.CapacityBytes}}}
	plan := planRPCDevices(selection, []RPCGuest{guest}, local.GGUFInfo{BlockCount: 42},
		Settings{ContextTokens: 2048}, 10*gib)
	if plan.DeviceArg() != "RPC0,RPC1" || plan.TensorSplitArg() == "" ||
		plan.Modes["node"] != "gpu+ram" {
		t.Fatalf("oversized model cannot use additive RAM and VRAM: %+v", plan)
	}
	if len(plan.Split) != 2 || plan.Split[0] == 0 || plan.Split[1] == 0 {
		t.Fatalf("native tensor split invalid: %+v", plan)
	}
}

func TestRPCPlacementPreservesLegacyWorkerAndMixedClusterSafety(t *testing.T) {
	const gib = uint64(1 << 30)
	gpu := RPCGuest{ID: "gpu", CapacityBytes: 10 * gib,
		RPCDeviceKinds: []string{"gpu", "cpu"}, GPUBudgetBytes: 3 * gib, RAMBudgetBytes: 7 * gib}
	legacy := RPCGuest{ID: "legacy", CapacityBytes: 10 * gib}
	selection := clusterplan.Selection{Guests: []clusterplan.Candidate{
		{ID: "gpu", CapacityBytes: gpu.CapacityBytes},
		{ID: "legacy", CapacityBytes: legacy.CapacityBytes},
	}}
	plan := planRPCDevices(selection, []RPCGuest{gpu, legacy}, local.GGUFInfo{BlockCount: 42},
		Settings{ContextTokens: 2048}, 8*gib)
	if len(plan.Devices) != 0 || plan.TensorSplitArg() != "" {
		t.Fatalf("unknown device counts must not break native RPC indices: %+v", plan)
	}
}

func TestRPCPlacementOffsetsDeviceNumbersAcrossGuests(t *testing.T) {
	const gib = uint64(1 << 30)
	first := RPCGuest{ID: "first", CapacityBytes: 13 * gib,
		RPCDeviceKinds: []string{"gpu", "cpu"}, GPUBudgetBytes: 4 * gib, RAMBudgetBytes: 9 * gib}
	second := RPCGuest{ID: "second", CapacityBytes: 8 * gib,
		RPCDeviceKinds: []string{"gpu"}, GPUBudgetBytes: 8 * gib}
	selection := clusterplan.Selection{LocalLayers: 0,
		Guests: []clusterplan.Candidate{
			{ID: first.ID, CapacityBytes: first.CapacityBytes},
			{ID: second.ID, CapacityBytes: second.CapacityBytes},
		},
	}
	plan := planRPCDevices(selection, []RPCGuest{first, second},
		local.GGUFInfo{BlockCount: 42}, Settings{ContextTokens: 2048}, 15*gib)
	if !strings.Contains(plan.DeviceArg(), "RPC2") {
		t.Fatalf("second Guest should be RPC2 after a two-device worker: %+v", plan)
	}
}

func TestRPCPlacementSupportsCPUGuestWithMeasuredMemory(t *testing.T) {
	const gib = uint64(1 << 30)
	guest := RPCGuest{ID: "cpu", CapacityBytes: 10 * gib, RPCDeviceKinds: []string{"cpu"}, RAMBudgetBytes: 10 * gib}
	selection := clusterplan.Selection{Guests: []clusterplan.Candidate{{ID: guest.ID, CapacityBytes: guest.CapacityBytes}}}
	plan := planRPCDevices(selection, []RPCGuest{guest}, local.GGUFInfo{BlockCount: 40}, Settings{ContextTokens: 2048}, 4*gib)
	if plan.DeviceArg() != "RPC0" || plan.Modes["cpu"] != "ram" {
		t.Fatalf("CPU-only worker was rejected: %+v", plan)
	}
}

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

func TestWithHostVulkanPrependsHostDeviceAndClearsLocalLayers(t *testing.T) {
	base := RPCDevicePlacement{
		Devices: []string{"RPC0"},
		Split:   []uint64{4096},
		Modes:   map[string]string{"guest": "gpu"},
	}
	got, local := withHostGPU(base, 50, 65, "vulkan", "Vulkan0", "auto")
	if local != 0 {
		t.Fatalf("expected localLayers=0 when Host uses Vulkan0, got %d", local)
	}
	if got.DeviceArg() != "Vulkan0,RPC0" || got.Modes["host"] != "vulkan" {
		t.Fatalf("unexpected Vulkan+RPC placement: %+v", got)
	}
	if len(got.Split) != 2 || got.Split[0] <= got.Split[1] {
		t.Fatalf("expected Host split > remote split for 50/15 layers, got %+v", got.Split)
	}
	// Disabled when host_gpu != vulkan or gpu_layers == 0.
	unchanged, loc2 := withHostGPU(base, 50, 65, "auto", "", "auto")
	if loc2 != 50 || unchanged.DeviceArg() != "RPC0" {
		t.Fatalf("auto host_gpu unexpectedly enabled Vulkan0: %+v %d", unchanged, loc2)
	}
	zeroGPU, loc3 := withHostGPU(base, 50, 65, "vulkan", "Vulkan0", "0")
	if loc3 != 50 || zeroGPU.DeviceArg() != "RPC0" {
		t.Fatalf("gpu_layers=0 unexpectedly enabled Vulkan0: %+v %d", zeroGPU, loc3)
	}
}

func TestHostGPUDiscoveryUsesNativeNameAndFallback(t *testing.T) {
	output := "Available devices:\n  MTL2: Apple GPU (8192 MiB, 4096 MiB free)\n"
	device := parseHostGPU(output, "metal")
	plan := RPCDevicePlacement{Devices: []string{"RPC0"}, Split: []uint64{10}}
	got, local := withHostGPU(plan, 20, 40, "metal", device, "auto")
	if local != 0 || got.DeviceArg() != "MTL2,RPC0" || got.Modes["host"] != "metal" {
		t.Fatalf("wrong confirmed Metal device: %+v %d", got, local)
	}
	if name := parseHostGPU(output+"  MTL3: GPU (4096 MiB, 2048 MiB free)\n", "metal"); name != "" {
		t.Fatal("ambiguous multi-GPU device must not be guessed")
	}
	unchanged, local := withHostGPU(plan, 20, 40, "metal", parseHostGPU("Available devices:\n  (none)\n", "metal"), "auto")
	if local != 20 || unchanged.DeviceArg() != "RPC0" {
		t.Fatalf("missing GPU must preserve CPU placement: %+v %d", unchanged, local)
	}
}

func TestDetailedLayerPlacementChecksIndividualDeviceBudgets(t *testing.T) {
	const mib = uint64(1 << 20)
	guest := RPCGuest{ID: "node", CapacityBytes: 200 * mib,
		RPCDeviceKinds: []string{"gpu", "cpu"}, GPUBudgetBytes: 60 * mib, RAMBudgetBytes: 140 * mib}
	meta := local.GGUFInfo{BlockCount: 3, BlockBytes: []uint64{40 * mib, 60 * mib, 20 * mib}}
	selection := clusterplan.Selection{Guests: []clusterplan.Candidate{{ID: guest.ID, CapacityBytes: guest.CapacityBytes}}, GuestLayers: []int{3}}
	plan := planRPCDevices(selection, []RPCGuest{guest}, meta, Settings{}, 120*mib)
	if plan.DeviceArg() != "RPC0,RPC1" || plan.TensorSplitArg() != "1,2" {
		t.Fatalf("unexpected detailed layer placement: %+v", plan)
	}
	guest.RAMBudgetBytes = 70 * mib
	if plan := planRPCDevices(selection, []RPCGuest{guest}, meta, Settings{}, 120*mib); len(plan.Devices) != 0 {
		t.Fatalf("unsafe device split was accepted: %+v", plan)
	}
}

func TestDetailedLayerPlacementPreservesLegacyTopology(t *testing.T) {
	t.Setenv("NODEXLM_GGUF_LAYER_BYTES", "1")
	meta := local.GGUFInfo{BlockCount: 3, BlockBytes: []uint64{10, 20, 30}}
	guest := RPCGuest{ID: "legacy", CapacityBytes: 100}
	selection := clusterplan.Selection{LocalLayers: 1, Guests: []clusterplan.Candidate{{ID: guest.ID, CapacityBytes: guest.CapacityBytes}}}
	if err := applyLayerBytes(meta, Settings{}, 60, 20, []RPCGuest{guest}, &selection); err != nil || selection.LocalLayers != 1 || len(selection.GuestLayers) != 0 {
		t.Fatalf("legacy placement changed: %+v, %v", selection, err)
	}
}

func TestCUDAHostRequiresConfirmedDevice(t *testing.T) {
	output := "Available devices:\n  CUDA0: NVIDIA GPU (6144 MiB, 5000 MiB free)\n"
	device := parseHostGPU(output, "cuda")
	plan := RPCDevicePlacement{Devices: []string{"RPC0"}, Split: []uint64{20}}
	got, layers := withHostGPU(plan, 20, 40, "cuda", device, "auto")
	if layers != 0 || got.DeviceArg() != "CUDA0,RPC0" || got.Modes["host"] != "cuda" {
		t.Fatalf("CUDA placement: %+v %d", got, layers)
	}
	if parseHostGPU(output+"  CUDA1: GPU (6144 MiB, 5000 MiB free)\n", "cuda") != "" {
		t.Fatal("ambiguous native CUDA devices")
	}
}

func TestMultiGPUPlacementUsesSeparateConservativeBudgets(t *testing.T) {
	const gib = uint64(1 << 30)
	guest := RPCGuest{ID: "multi", CapacityBytes: 16 * gib, RPCDeviceKinds: []string{"gpu", "gpu", "cpu"}, GPUBudgetBytes: 8 * gib, RAMBudgetBytes: 8 * gib}
	selection := clusterplan.Selection{Guests: []clusterplan.Candidate{{ID: guest.ID, CapacityBytes: guest.CapacityBytes}}}
	meta := local.GGUFInfo{BlockCount: 10}
	plan := planRPCDevices(selection, []RPCGuest{guest}, meta, Settings{}, 6*gib)
	if plan.DeviceArg() != "RPC0,RPC1" || plan.Modes[guest.ID] != "gpu" || len(plan.Split) != 2 || plan.Split[0]+plan.Split[1] != 10 {
		t.Fatalf("multi GPU split: %+v", plan)
	}
	plan = planRPCDevices(selection, []RPCGuest{guest}, meta, Settings{}, 10*gib)
	if plan.DeviceArg() != "RPC0,RPC1,RPC2" || plan.Modes[guest.ID] != "gpu+ram" {
		t.Fatalf("multi GPU + RAM split: %+v", plan)
	}
	if plan := planRPCDevices(selection, []RPCGuest{guest}, meta, Settings{}, 20*gib); len(plan.Devices) != 0 {
		t.Fatalf("unsafe multi-GPU split: %+v", plan)
	}
}

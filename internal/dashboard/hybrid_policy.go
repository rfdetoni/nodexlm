package dashboard

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
)

// RPCDevicePlacement is the exact native GGML device subset to use for this
// load. Device indices are allocated by llama.cpp in --rpc endpoint order.
// A nil device list means legacy RPC telemetry is insufficient for a safe
// choice, so native default placement is preserved.
type RPCDevicePlacement struct {
	Devices []string
	Split   []uint64
	Modes   map[string]string
}

func (p RPCDevicePlacement) DeviceArg() string {
	return strings.Join(p.Devices, ",")
}

func (p RPCDevicePlacement) TensorSplitArg() string {
	if len(p.Split) <= 1 {
		return ""
	}
	parts := make([]string, len(p.Split))
	for i, v := range p.Split {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ",")
}

func rpcGPUCount(guest RPCGuest) int {
	n := 0
	for _, kind := range guest.RPCDeviceKinds {
		if kind == "gpu" {
			n++
		}
	}
	return n
}

func rpcDeviceBudget(guest RPCGuest, kind string) uint64 {
	if kind == "cpu" {
		return guest.RAMBudgetBytes
	}
	if n := rpcGPUCount(guest); kind == "gpu" && n > 0 {
		return guest.GPUBudgetBytes / uint64(n)
	}
	return 0
}

func validRPCDeviceKinds(guest RPCGuest) bool {
	kinds := guest.RPCDeviceKinds
	if len(kinds) == 0 || len(kinds) > 17 || rpcGPUCount(guest) > 16 {
		return false
	}
	for i, kind := range kinds {
		if kind != "cpu" && kind != "gpu" || kind == "cpu" && i != len(kinds)-1 || rpcDeviceBudget(guest, kind) == 0 {
			return false
		}
	}
	return true
}

func selectedMultiGPU(selection clusterplan.Selection, available []RPCGuest) bool {
	for _, chosen := range selection.Guests {
		for _, guest := range available {
			if chosen.ID == guest.ID && rpcGPUCount(guest) > 1 {
				return true
			}
		}
	}
	return false
}

// planRPCDevices chooses a GPU-only device when it can safely carry the
// assigned model and context. CPU/RAM is a fallback capacity pool, not a
// default fraction just because the Guest has more RAM than VRAM.
//
// This is intentionally not an independent speed prediction: native
// --tensor-split determines layer placement, and runtime buffers vary by
// architecture. Benchmarks decide whether offloading beats Host-only.
func planRPCDevices(selection clusterplan.Selection, available []RPCGuest, meta local.GGUFInfo, s Settings, modelBytes uint64) RPCDevicePlacement {
	if len(selection.Guests) == 0 || meta.BlockCount == 0 || modelBytes == 0 {
		return RPCDevicePlacement{}
	}
	byID := make(map[string]RPCGuest, len(available))
	for _, guest := range available {
		byID[guest.ID] = guest
	}
	// All selected Guests must have known, compatible native topology.
	// With even one legacy Guest, never guess global RPC0/RPC1 indices.
	for _, chosen := range selection.Guests {
		guest, ok := byID[chosen.ID]
		if !ok || !validRPCDeviceKinds(guest) {
			return RPCDevicePlacement{}
		}
	}
	result := RPCDevicePlacement{Modes: make(map[string]string)}
	transient := estimateTransientBytes(meta, s)
	totalBudget := uint64(0)
	for _, chosen := range selection.Guests {
		if totalBudget > ^uint64(0)-chosen.CapacityBytes {
			return RPCDevicePlacement{}
		}
		totalBudget += chosen.CapacityBytes
	}
	if totalBudget == 0 {
		return RPCDevicePlacement{}
	}
	remoteLayers := int(meta.BlockCount) - selection.LocalLayers
	if remoteLayers <= 0 {
		return RPCDevicePlacement{}
	}
	multi := selectedMultiGPU(selection, available)
	remainingLayers := remoteLayers
	remainingBudget := totalBudget
	rpcIndex := 0
	for i, chosen := range selection.Guests {
		guest := byID[chosen.ID]
		layers := remainingLayers
		if i != len(selection.Guests)-1 {
			layers = int(uint64(remainingLayers) * chosen.CapacityBytes / remainingBudget)
		}
		detailed := len(selection.GuestLayers) == len(selection.Guests)
		if detailed {
			layers = selection.GuestLayers[i]
			if layers <= 0 || layers > remainingLayers {
				return RPCDevicePlacement{}
			}
		}
		start := int(meta.BlockCount) - remainingLayers
		remainingLayers -= layers
		remainingBudget -= chosen.CapacityBytes

		// Reserve for approximate weights and KV per assigned layers.
		if uint64(layers) > 0 && modelBytes > ^uint64(0)/uint64(layers) {
			return RPCDevicePlacement{}
		}
		weight := modelBytes * uint64(layers) / uint64(meta.BlockCount)
		if detailed || multi {
			// Use real per-layer weights plus context, with the same 115% margin.
			fits := func(offset, count int, budget uint64) bool {
				w := estimatedBlockWeight(meta, modelBytes, offset, count)
				if w > ^uint64(0)/115 {
					return false
				}
				need := w * 115 / 100
				if transient != nil {
					if *transient > ^uint64(0)/uint64(count) {
						return false
					}
					kv := *transient * uint64(count) / uint64(meta.BlockCount)
					if kv > ^uint64(0)-need {
						return false
					}
					need += kv
				}
				return need <= budget
			}
			left, offset := layers, start
			gpuUsed, cpuUsed := false, false
			for i, kind := range guest.RPCDeviceKinds {
				budget := rpcDeviceBudget(guest, kind)
				count := 0
				for count < left && fits(offset, count+1, budget) {
					count++
				}
				if count == 0 {
					continue
				}
				result.Devices = append(result.Devices, "RPC"+fmt.Sprint(rpcIndex+i))
				result.Split = append(result.Split, uint64(count))
				gpuUsed = gpuUsed || kind == "gpu"
				cpuUsed = cpuUsed || kind == "cpu"
				offset += count
				left -= count
			}
			if left != 0 || layers == 0 {
				return RPCDevicePlacement{}
			}
			switch {
			case gpuUsed && cpuUsed:
				result.Modes[guest.ID] = "gpu+ram"
			case gpuUsed:
				result.Modes[guest.ID] = "gpu"
			default:
				result.Modes[guest.ID] = "ram"
			}
			rpcIndex += len(guest.RPCDeviceKinds)
			continue
		}
		if weight > ^uint64(0)/105 {
			return RPCDevicePlacement{}
		}
		// CPU/RAM is not selected unless VRAM cannot cover the model,
		// explicit KV/batch estimate, and a small tensor overhead.
		// GPU device budget already reserves headroom separately.
		required := weight * 105 / 100
		if transient != nil {
			if *transient > ^uint64(0)/uint64(layers+1) {
				return RPCDevicePlacement{}
			}
			context := *transient * uint64(layers) / uint64(meta.BlockCount)
			if context > ^uint64(0)-required {
				return RPCDevicePlacement{}
			}
			required += context
		}
		// Weights and context on the GPU are both bounded by the GPU's
		// own memory reserve, not by total RAM+VRAM reported by the OS.
		first := "RPC" + fmt.Sprint(rpcIndex)
		switch {
		case len(guest.RPCDeviceKinds) == 1 && guest.RPCDeviceKinds[0] == "cpu":
			result.Devices = append(result.Devices, first)
			result.Split = append(result.Split, max(uint64(1), guest.RAMBudgetBytes/clusterplan.MiB))
			result.Modes[guest.ID] = "ram"
		case len(guest.RPCDeviceKinds) == 1:
			result.Devices = append(result.Devices, first)
			result.Split = append(result.Split, max(uint64(1), guest.GPUBudgetBytes/clusterplan.MiB))
			result.Modes[guest.ID] = "gpu"
		case required <= guest.GPUBudgetBytes:
			result.Devices = append(result.Devices, first)
			result.Split = append(result.Split, max(uint64(1), guest.GPUBudgetBytes/clusterplan.MiB))
			result.Modes[guest.ID] = "gpu"
		default:
			// GPU first, RAM for the remainder. Values are proportions,
			// not byte allocations; the native loader performs the real
			// placement. Reserve GPU headroom for kernels and scratch.
			gpuMiB := max(uint64(1), guest.GPUBudgetBytes/clusterplan.MiB)
			remainingMiB := max(uint64(1), (required-guest.GPUBudgetBytes+clusterplan.MiB-1)/clusterplan.MiB)
			if remainingMiB > max(uint64(1), guest.RAMBudgetBytes/clusterplan.MiB) {
				// Planner admission and native capacity disagree; safer
				// to use the worker's standard split instead.
				return RPCDevicePlacement{}
			}
			result.Devices = append(result.Devices, first, "RPC"+fmt.Sprint(rpcIndex+1))
			result.Split = append(result.Split, gpuMiB, remainingMiB)
			result.Modes[guest.ID] = "gpu+ram"
		}
		rpcIndex += len(guest.RPCDeviceKinds)
	}
	return result
}

func effectiveHostRuntimeBackend(hostGPU string) string {
	switch hostGPU {
	case "vulkan", "cpu", "metal", "cuda":
		return hostGPU
	default:
		return "auto"
	}
}

// discoverHostGPU uses the resolved llama-server registry rather than guessed names.
func discoverHostGPU(ctx context.Context, binary, backend string) string {
	if backend != "vulkan" && backend != "metal" && backend != "cuda" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--list-devices")
	cmd.Env = llamaruntime.NativeEnvironment(binary)
	out, err := cmd.CombinedOutput()
	if err != nil || ctx.Err() != nil {
		return ""
	}
	return parseHostGPU(string(out), backend)
}

func parseHostGPU(output, backend string) string {
	if backend != "vulkan" && backend != "metal" && backend != "cuda" {
		return ""
	}
	inList := false
	found := ""
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "Available devices:" {
			inList = true
			continue
		}
		if !inList {
			continue
		}
		name, description, ok := strings.Cut(line, ":")
		if !ok || !strings.Contains(description, "MiB free)") {
			if found != "" {
				break
			}
			continue
		}
		// Pinned ggml-metal-device.m names devices MTL<n>.
		prefix := backend
		if backend == "metal" {
			prefix = "mtl"
		}
		if !strings.HasPrefix(strings.ToLower(name), prefix) || strings.ContainsAny(name, " \t,\r\n") {
			continue
		}
		if found != "" && found != name {
			return "" // do not guess multi-GPU mapping
		}
		found = name
	}
	return found
}

// withHostGPU prepends only a confirmed native device, preserving split math.
func withHostGPU(plan RPCDevicePlacement, localLayers int, blocks uint32, backend, device, gpuLayers string) (RPCDevicePlacement, int) {
	if (backend != "vulkan" && backend != "metal" && backend != "cuda") || device == "" || gpuLayers == "0" || localLayers <= 0 || int(blocks) <= localLayers || len(plan.Devices) == 0 {
		return plan, localLayers
	}
	remoteLayers := uint64(int(blocks) - localLayers)
	var hostSplit uint64
	if len(plan.Split) == len(plan.Devices) && len(plan.Split) > 0 {
		var remoteSum uint64
		for _, v := range plan.Split {
			remoteSum += v
		}
		hostSplit = max(uint64(1), (remoteSum*uint64(localLayers)+remoteLayers/2)/remoteLayers)
		plan.Split = append([]uint64{hostSplit}, plan.Split...)
	} else {
		plan.Split = []uint64{uint64(localLayers), remoteLayers}
	}
	plan.Devices = append([]string{device}, plan.Devices...)
	if plan.Modes == nil {
		plan.Modes = make(map[string]string)
	}
	plan.Modes["host"] = backend
	return plan, 0
}

func blockWeight(meta local.GGUFInfo, start, count int) uint64 {
	var total uint64
	for _, weight := range meta.BlockBytes[start : start+count] {
		total += weight
	}
	return total
}

// Use conservative rounded-up uniform weights only when tensor metadata is absent.
func estimatedBlockWeight(meta local.GGUFInfo, size uint64, start, count int) uint64 {
	if len(meta.BlockBytes) == int(meta.BlockCount) {
		return blockWeight(meta, start, count)
	}
	blocks := uint64(meta.BlockCount)
	if blocks == 0 {
		return 0
	}
	per := size/blocks + min(uint64(1), size%blocks)
	return per * uint64(count)
}

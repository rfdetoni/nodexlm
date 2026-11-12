package dashboard

import (
	"fmt"
	"strings"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
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

func validRPCDeviceKinds(guest RPCGuest) bool {
	kinds := guest.RPCDeviceKinds
	if len(kinds) == 0 || len(kinds) > 2 {
		return false
	}
	for i, kind := range kinds {
		if kind != "cpu" && kind != "gpu" {
			return false
		}
		if i == 1 && (kinds[0] != "gpu" || kind != "cpu") {
			return false
		}
	}
	if kinds[0] == "gpu" && guest.GPUBudgetBytes == 0 {
		return false
	}
	if kinds[0] == "cpu" && guest.RAMBudgetBytes == 0 {
		return false
	}
	if len(kinds) == 2 && guest.RAMBudgetBytes == 0 {
		return false
	}
	return true
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
	remainingLayers := remoteLayers
	remainingBudget := totalBudget
	rpcIndex := 0
	for i, chosen := range selection.Guests {
		guest := byID[chosen.ID]
		layers := remainingLayers
		if i != len(selection.Guests)-1 {
			layers = int(uint64(remainingLayers) * chosen.CapacityBytes / remainingBudget)
		}
		remainingLayers -= layers
		remainingBudget -= chosen.CapacityBytes

		// Reserve for approximate weights and KV per assigned layers.
		weight := modelBytes * uint64(layers) / uint64(meta.BlockCount)
		if weight > ^uint64(0)/115 {
			return RPCDevicePlacement{}
		}
		required := weight * 115 / 100
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

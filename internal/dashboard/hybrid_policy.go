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

// NativeTensorSplitArg accounts for the output pseudo-layer in llama.cpp's
// denominator. The planner's Split values count transformer blocks only.
func (p RPCDevicePlacement) NativeTensorSplitArg() string {
	if len(p.Split) <= 1 {
		return ""
	}
	native := p
	native.Split = append([]uint64(nil), p.Split...)
	native.Split[len(native.Split)-1]++
	return native.TensorSplitArg()
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
	if len(selection.Guests) == 0 || meta.BlockCount == 0 || meta.BlockCount > 4096 || modelBytes == 0 || modelBytes > ^uint64(0)/115 || selection.LocalLayers < 0 || selection.LocalLayers >= int(meta.BlockCount) {
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
	if s.RPCNativeScheduler && (len(meta.BlockBytes) != int(meta.BlockCount) || meta.NonBlockBytes == 0) {
		return RPCDevicePlacement{}
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

		// Split weights must all have the same unit: layers, not a mixture
		// of available capacity and estimated allocated MiB. Otherwise the
		// native normalization silently moves layers across Guests.
		if layers == 0 {
			rpcIndex += len(guest.RPCDeviceKinds)
			continue
		}
		margin := uint64(105)
		if detailed || multi {
			margin = 115
		}
		fits := func(offset, count int, budget uint64) bool {
			w := estimatedBlockWeight(meta, modelBytes, offset, count)
			// Conservatively reserve all non-block weights on the last device,
			// including a possible duplicate tied output embedding.
			if s.RPCNativeScheduler && offset+count == int(meta.BlockCount) {
				if meta.NonBlockBytes > ^uint64(0)-w {
					return false
				}
				w += meta.NonBlockBytes
			}
			if w > ^uint64(0)/margin {
				return false
			}
			need := w * margin / 100
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
		if !fits(start, layers, chosen.CapacityBytes) {
			return RPCDevicePlacement{}
		}
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
		if left != 0 {
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
	}
	if remainingLayers != 0 {
		return RPCDevicePlacement{}
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

// preferGPUAcrossGuests tries one conservative alternative to the existing
// memory-proportional split. It preserves the selected Guests and Host share,
// validates actual contiguous layers, and only adopts a larger GPU share.
// This is a VRAM-priority heuristic, not an estimate of measured tokens/s.
func preferGPUAcrossGuests(selection clusterplan.Selection, available []RPCGuest, meta local.GGUFInfo, s Settings, size uint64) clusterplan.Selection {
	n := len(selection.Guests)
	blocks := int(meta.BlockCount)
	remote := blocks - selection.LocalLayers
	if n < 2 || remote < n || blocks <= 0 || blocks > 4096 || size == 0 || size > ^uint64(0)/115 ||
		explicitPrimary(s.PrimaryNode) || len(selection.GuestLayers) != 0 || !knownRPCPlacement(selection, available) {
		return selection
	}
	unit := size/uint64(blocks) + min(uint64(1), size%uint64(blocks))
	unit = (unit*115 + 99) / 100
	if transient := estimateTransientBytes(meta, s); transient != nil {
		unit += *transient/uint64(blocks) + min(uint64(1), *transient%uint64(blocks))
	}
	if unit == 0 {
		return selection
	}
	byID := make(map[string]RPCGuest, len(available))
	for _, g := range available {
		byID[g.ID] = g
	}
	shares, caps, gpuCaps := make([]int, n), make([]int, n), make([]int, n)
	for i, chosen := range selection.Guests {
		guest := byID[chosen.ID]
		for _, kind := range guest.RPCDeviceKinds {
			count := int(min(uint64(remote), rpcDeviceBudget(guest, kind)/unit))
			caps[i] += count
			if kind == "gpu" {
				gpuCaps[i] += count
			}
		}
		caps[i] = min(caps[i], int(min(uint64(remote), chosen.CapacityBytes/unit)))
		gpuCaps[i] = min(gpuCaps[i], caps[i])
		if caps[i] == 0 {
			return selection
		}
		shares[i] = 1 // keep all admitted Guests participating
	}
	left := remote - n
	for i := range shares {
		add := min(left, max(0, gpuCaps[i]-shares[i]))
		shares[i] += add
		left -= add
	}
	// Remaining CPU/RAM layers go to the largest remaining safe capacity.
	// This avoids arbitrary CPU speed assumptions from VRAM size alone.
	for left > 0 {
		best := -1
		for i := range shares {
			if caps[i] > shares[i] && (best == -1 || caps[i]-shares[i] > caps[best]-shares[best]) {
				best = i
			}
		}
		if best < 0 {
			return selection
		}
		add := min(left, caps[best]-shares[best])
		shares[best] += add
		left -= add
	}
	candidate := selection
	candidate.GuestLayers = shares
	next := planRPCDevices(candidate, available, meta, s, size)
	if len(next.Devices) == 0 {
		return selection
	}
	previous := planRPCDevices(selection, available, meta, s, size)
	gpuNames := make(map[string]bool)
	index := 0
	for _, chosen := range selection.Guests {
		for _, kind := range byID[chosen.ID].RPCDeviceKinds {
			gpuNames[fmt.Sprint("RPC", index)] = kind == "gpu"
			index++
		}
	}
	gpuLayers := func(p RPCDevicePlacement) uint64 {
		var total uint64
		for i, name := range p.Devices {
			if gpuNames[name] {
				total += p.Split[i]
			}
		}
		return total
	}
	if len(previous.Devices) != 0 && gpuLayers(next) <= gpuLayers(previous) {
		return selection
	}
	candidate.Reason += "; VRAM-priority alternative across Guests (estimated; benchmark to verify throughput)"
	return candidate
}

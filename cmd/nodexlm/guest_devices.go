package main

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// Only separate, measured VRAM is proof that a GPU can contribute more
// capacity than system RAM. A shared-memory GPU must not inflate RAM totals.
func guestDedicatedGPU(profile protocol.Device) bool {
	for _, gpu := range profile.GPUs {
		if gpu.MemoryType == "dedicated" && gpu.FreeVRAMBytes >= 1<<30 {
			return true
		}
	}
	return false
}

func autoGuestBackend(profile protocol.Device) string {
	// The published Linux AMD64 worker has a verified Vulkan + RPC bundle.
	// Do not force source builds or assume an installed GPU driver on other
	// systems; explicit --backend vulkan remains available everywhere.
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && guestDedicatedGPU(profile) {
		return "vulkan"
	}
	if runtime.GOOS == "darwin" {
		return "metal"
	}
	return "cpu"
}

// Probe the exact pinned native runtime, not a nvidia-smi guess: its GGML
// registry determines the valid --device names. Asking for a non-existent
// device prints the available list and exits before binding a listening socket.
func nativeRPCDeviceNames(binary string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--device", "__nodexlm_probe_devices__")
	cmd.Env = llamaruntime.NativeEnvironment(binary)
	output, _ := cmd.CombinedOutput()
	if ctx.Err() != nil {
		slog.Warn("RPC device discovery timed out; using native device fallback", "error", ctx.Err())
		return nil
	}
	return parseRPCDeviceNames(string(output))
}

func parseRPCDeviceNames(output string) []string {
	inList := false
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "available devices:") {
			inList = true
			continue
		}
		if !inList {
			continue
		}
		name, _, ok := strings.Cut(trimmed, ":")
		if !ok || strings.ContainsAny(name, " \t") {
			if len(names) > 0 {
				break
			}
			continue
		}
		if name == "CPU" || strings.HasPrefix(name, "Vulkan") ||
			strings.HasPrefix(name, "CUDA") || strings.HasPrefix(name, "Metal") {
			if !seen[name] {
				names = append(names, name)
				seen[name] = true
			}
		}
	}
	return names
}

func nativeDeviceKinds(device string, profile protocol.Device) []string {
	var kinds []string
	for _, token := range strings.Split(device, ",") {
		name := strings.TrimSpace(token)
		switch {
		case name == "CPU":
			kinds = append(kinds, "cpu")
		case strings.HasPrefix(name, "CUDA"), strings.HasPrefix(name, "Vulkan"), strings.HasPrefix(name, "Metal"):
			// Only advertise additive GPU+RAM capacity for independently
			// measured dedicated GPU memory; shared heaps remain legacy.
			if !guestDedicatedGPU(profile) {
				return nil
			}
			kinds = append(kinds, "gpu")
		default:
			return nil // Do not guess budgets for a custom native device name.
		}
	}
	return kinds
}

func autoRPCDeviceChoice(available []string, profile protocol.Device) string {
	// Expose GPU devices first and one CPU device last. The Host selects
	// GPU-only when the model fits and GPU+CPU only when needed.
	var gpus []string
	cpu := false
	for _, name := range available {
		if name == "CPU" {
			cpu = true
		} else if strings.HasPrefix(name, "Vulkan") ||
			strings.HasPrefix(name, "CUDA") || strings.HasPrefix(name, "Metal") {
			gpus = append(gpus, name)
		}
	}
	if len(gpus) > 0 && guestDedicatedGPU(profile) {
		// Only advertise one GPU automatically until we can correlate
		// native GGML device IDs with nvidia-smi indices for multi-GPU.
		if cpu {
			return gpus[0] + ",CPU"
		}
		return gpus[0]
	}
	if cpu {
		return "CPU"
	}
	// An explicit Vulkan/iGPU Guest can still use the native backend's
	// normal accelerator selection without claiming additive memory.
	return ""
}

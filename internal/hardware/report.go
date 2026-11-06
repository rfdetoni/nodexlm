// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package hardware

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func command(timeout time.Duration, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
func memoryLinux() (uint64, uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var total, available uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		var kb uint64
		if strings.HasPrefix(line, "MemTotal:") {
			fmt.Sscanf(line, "MemTotal: %d kB", &kb)
			total = kb * 1024
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			fmt.Sscanf(line, "MemAvailable: %d kB", &kb)
			available = kb * 1024
		}
	}
	return total, available
}
func memoryDarwin() (uint64, uint64) {
	raw := command(1*time.Second, "sysctl", "-n", "hw.memsize")
	n, _ := strconv.ParseUint(raw, 10, 64)
	// Do not invent "free RAM" from total RAM: memory pressure is dynamic.
	return n, 0
}
func gpuNVIDIA() []protocol.GPUDevice {
	out := command(2*time.Second, "nvidia-smi", "--query-gpu=name,memory.total,memory.free", "--format=csv,noheader,nounits")
	if out == "" {
		return nil
	}
	var result []protocol.GPUDevice
	for _, line := range strings.Split(out, "\n") {
		columns := strings.Split(line, ",")
		if len(columns) != 3 {
			continue
		}
		total, er1 := strconv.ParseUint(strings.TrimSpace(columns[1]), 10, 64)
		free, er2 := strconv.ParseUint(strings.TrimSpace(columns[2]), 10, 64)
		if er1 != nil || er2 != nil {
			continue
		}
		result = append(result, protocol.GPUDevice{Name: strings.TrimSpace(columns[0]), Vendor: "nvidia", VRAMBytes: total << 20, FreeVRAMBytes: free << 20})
	}
	return result
}

// Collect only advertises measured hardware. Unknown free memory remains zero,
// preventing the coordinator from promising unsupported distributed placements.
func Collect(name string, models []string) protocol.Device {
	d := protocol.Device{Name: name, Platform: runtime.GOOS, Architecture: runtime.GOARCH, CPUCount: runtime.NumCPU(), Models: models}
	switch runtime.GOOS {
	case "linux":
		d.RAMBytes, d.AvailableRAMBytes = memoryLinux()
	case "darwin":
		d.RAMBytes, d.AvailableRAMBytes = memoryDarwin()
	}
	d.GPUs = gpuNVIDIA()
	d.Runtime = protocol.RuntimeCapability{Engine: "llama.cpp", Version: llamaruntime.Version}
	if _, _, err := llamaruntime.Installed(runtimeHome(), "auto"); err == nil {
		d.Runtime.Installed = true
	}
	return d
}

var runtimeHome = func() string {
	if v := os.Getenv("NODEXLM_HOME"); v != "" {
		return v
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ".nodexlm"
	}
	return dir + string(os.PathSeparator) + ".nodexlm"
}

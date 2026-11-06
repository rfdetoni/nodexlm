// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package hardware

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
	// vm_stat reports currently reclaimable pages, unlike hw.memsize alone.
	stats := command(time.Second, "vm_stat")
	pageSize := uint64(4096)
	if match := regexp.MustCompile(`page size of (\d+) bytes`).FindStringSubmatch(stats); len(match) == 2 {
		if value, err := strconv.ParseUint(match[1], 10, 64); err == nil && value >= 4096 && value <= 65536 {
			pageSize = value
		}
	}
	var pages uint64
	for _, line := range strings.Split(stats, "\n") {
		for _, prefix := range []string{"Pages free:", "Pages inactive:", "Pages speculative:"} {
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), prefix), "."))
				if v, err := strconv.ParseUint(value, 10, 64); err == nil {
					pages += v
				}
			}
		}
	}
	available := pages * pageSize
	if available > n {
		available = n
	}
	return n, available
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
	case "windows":
		d.RAMBytes, d.AvailableRAMBytes = memoryWindows()
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

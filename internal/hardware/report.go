// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package hardware

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	out := command(2*time.Second, "nvidia-smi", "--query-gpu=name,memory.total,memory.free,utilization.gpu", "--format=csv,noheader,nounits")
	if out == "" {
		return nil
	}
	var result []protocol.GPUDevice
	for _, line := range strings.Split(out, "\n") {
		columns := strings.Split(line, ",")
		if len(columns) != 4 {
			continue
		}
		total, er1 := strconv.ParseUint(strings.TrimSpace(columns[1]), 10, 64)
		free, er2 := strconv.ParseUint(strings.TrimSpace(columns[2]), 10, 64)
		if er1 != nil || er2 != nil {
			continue
		}
		gpu := protocol.GPUDevice{Name: strings.TrimSpace(columns[0]), Vendor: "nvidia", VRAMBytes: total << 20, FreeVRAMBytes: free << 20}
		if free <= total {
			used := (total - free) << 20
			gpu.VRAMUsedBytes = &used
		}
		if usage, err := strconv.ParseFloat(strings.TrimSpace(columns[3]), 64); err == nil && usage >= 0 && usage <= 100 {
			gpu.UsagePercent = &usage
		}
		result = append(result, gpu)
	}
	return result
}

// DRM supplies measured VRAM of AMD and Intel GPUs on Linux. Shared-memory
// iGPUs often omit dedicated VRAM counters; never fabricate a free value.
func drmDevices(root string) []protocol.GPUDevice {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var devices []protocol.GPUDevice
	for _, entry := range entries {
		card := entry.Name()
		if !strings.HasPrefix(card, "card") || len(card) < 5 {
			continue
		}
		if _, err := strconv.Atoi(card[4:]); err != nil {
			continue
		}
		device := filepath.Join(root, card, "device")
		vendorRaw, err := os.ReadFile(filepath.Join(device, "vendor"))
		if err != nil {
			continue
		}
		vendor := ""
		switch strings.ToLower(strings.TrimSpace(string(vendorRaw))) {
		case "0x1002":
			vendor = "amd"
		case "0x8086":
			vendor = "intel"
		default:
			continue
		}
		totalRaw, _ := os.ReadFile(filepath.Join(device, "mem_info_vram_total"))
		usedRaw, usedErr := os.ReadFile(filepath.Join(device, "mem_info_vram_used"))
		total, e1 := strconv.ParseUint(strings.TrimSpace(string(totalRaw)), 10, 64)
		used, e2 := strconv.ParseUint(strings.TrimSpace(string(usedRaw)), 10, 64)
		if e1 != nil {
			total = 0
		}
		free := uint64(0)
		if usedErr == nil && e2 == nil && used <= total {
			free = total - used
		}
		gpu := protocol.GPUDevice{
			Name: strings.ToUpper(vendor) + " " + card, Vendor: vendor,
			VRAMBytes: total, FreeVRAMBytes: free,
		}
		if usedErr == nil && e2 == nil && total > 0 && used <= total {
			value := used
			gpu.VRAMUsedBytes = &value
		}
		// AMD exposes an actual engine-busy sample; Intel or older DRM drivers
		// may not. Do not present missing counters as zero utilization.
		if busyRaw, err := os.ReadFile(filepath.Join(device, "gpu_busy_percent")); err == nil {
			if busy, err := strconv.ParseFloat(strings.TrimSpace(string(busyRaw)), 64); err == nil && busy >= 0 && busy <= 100 {
				gpu.UsagePercent = &busy
			}
		}
		devices = append(devices, gpu)
	}
	return devices
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
	d.CPUUsagePercent = sampleCPU()
	if d.RAMBytes > 0 && d.AvailableRAMBytes <= d.RAMBytes {
		used := d.RAMBytes - d.AvailableRAMBytes
		d.RAMUsedBytes = &used
	}
	d.GPUs = gpuNVIDIA()
	if runtime.GOOS == "linux" {
		d.GPUs = append(d.GPUs, drmDevices("/sys/class/drm")...)
	}
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

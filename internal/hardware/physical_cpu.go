// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package hardware

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PhysicalCPUCount returns physical CPU cores on Linux (avoiding SMT/HT
// contention in llama.cpp) and falls back to NumCPU-1 elsewhere.
func PhysicalCPUCount() int {
	if runtime.GOOS == "linux" {
		if n := physicalCPUCountFrom("/sys/devices/system/cpu"); n > 0 {
			return n
		}
	}
	return max(1, runtime.NumCPU()-1)
}

func physicalCPUCountFrom(root string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	type coreKey struct{ pkg, core string }
	seen := make(map[coreKey]bool)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "cpu") || len(name) < 4 || name[3] < '0' || name[3] > '9' {
			continue
		}
		topo := filepath.Join(root, name, "topology")
		coreRaw, err := os.ReadFile(filepath.Join(topo, "core_id"))
		if err != nil {
			continue
		}
		pkgRaw, _ := os.ReadFile(filepath.Join(topo, "physical_package_id"))
		seen[coreKey{
			pkg:  strings.TrimSpace(string(pkgRaw)),
			core: strings.TrimSpace(string(coreRaw)),
		}] = true
	}
	return len(seen)
}

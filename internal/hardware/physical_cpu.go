// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package hardware

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// PhysicalCPUCount returns faster physical CPU cores on Linux (avoiding SMT/HT
// and slower E-cores that stall GGML synchronization barriers) and falls back
// elsewhere.
func PhysicalCPUCount() int {
	if runtime.GOOS == "linux" {
		if n := physicalCPUCountFrom("/sys/devices/system/cpu"); n > 0 {
			return min(n, runtime.NumCPU())
		}
	}
	if n := platformPhysicalCPUCount(); n > 0 {
		return min(n, runtime.NumCPU())
	}
	return max(1, runtime.NumCPU()-1)
}

func parseCPUList(raw string) map[int]bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	result := make(map[int]bool)
	for _, token := range strings.Split(trimmed, ",") {
		part := strings.TrimSpace(token)
		if part == "" {
			return nil
		}
		if startStr, endStr, isRange := strings.Cut(part, "-"); isRange {
			lo, err1 := strconv.Atoi(startStr)
			hi, err2 := strconv.Atoi(endStr)
			if err1 != nil || err2 != nil || lo < 0 || hi > 4096 || lo > hi {
				return nil
			}
			for id := lo; id <= hi; id++ {
				result[id] = true
			}
		} else {
			id, err := strconv.Atoi(part)
			if err != nil || id < 0 || id > 4096 {
				return nil
			}
			result[id] = true
		}
	}
	return result
}

func readSysfsUint(path string) uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	val, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0
	}
	return val
}

func physicalCPUCountFrom(root string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	var pCores map[int]bool
	if raw, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(root)), "cpu_core", "cpus")); err == nil {
		pCores = parseCPUList(string(raw))
	}
	type coreKey struct{ pkg, core string }
	type coreStats struct{ cap, maxFreq uint64 }
	cores := make(map[coreKey]*coreStats)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "cpu") || len(name) < 4 || name[3] < '0' || name[3] > '9' {
			continue
		}
		cpuIdx, err := strconv.Atoi(name[3:])
		if err != nil || (len(pCores) > 0 && !pCores[cpuIdx]) {
			continue
		}
		if raw, err := os.ReadFile(filepath.Join(root, name, "online")); err == nil && strings.TrimSpace(string(raw)) == "0" {
			continue
		}
		topo := filepath.Join(root, name, "topology")
		coreRaw, err := os.ReadFile(filepath.Join(topo, "core_id"))
		if err != nil {
			continue
		}
		pkgRaw, _ := os.ReadFile(filepath.Join(topo, "physical_package_id"))
		key := coreKey{
			pkg:  strings.TrimSpace(string(pkgRaw)),
			core: strings.TrimSpace(string(coreRaw)),
		}
		stats := cores[key]
		if stats == nil {
			stats = &coreStats{}
			cores[key] = stats
		}
		if c := readSysfsUint(filepath.Join(root, name, "cpu_capacity")); c > stats.cap {
			stats.cap = c
		}
		if f := readSysfsUint(filepath.Join(root, name, "cpufreq", "cpuinfo_max_freq")); f > stats.maxFreq {
			stats.maxFreq = f
		}
	}
	if len(cores) == 0 {
		return 0
	}
	allCap, allFreq := true, true
	var minCap, maxCap, minFreq, maxFreq uint64
	for _, s := range cores {
		if s.cap == 0 {
			allCap = false
		} else {
			if minCap == 0 || s.cap < minCap {
				minCap = s.cap
			}
			if s.cap > maxCap {
				maxCap = s.cap
			}
		}
		if s.maxFreq == 0 {
			allFreq = false
		} else {
			if minFreq == 0 || s.maxFreq < minFreq {
				minFreq = s.maxFreq
			}
			if s.maxFreq > maxFreq {
				maxFreq = s.maxFreq
			}
		}
	}
	if allCap && maxCap > 0 && minCap < maxCap*70/100 {
		threshold := maxCap * 70 / 100
		faster := 0
		for _, s := range cores {
			if s.cap >= threshold {
				faster++
			}
		}
		return faster
	}
	if allFreq && maxFreq > 0 && minFreq < maxFreq*85/100 {
		threshold := maxFreq * 85 / 100
		faster := 0
		for _, s := range cores {
			if s.maxFreq >= threshold {
				faster++
			}
		}
		return faster
	}
	return len(cores)
}

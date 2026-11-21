// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package smolmax

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// RecommendedThreads returns the fastest CPU cluster's size on big.LITTLE
// systems. Sysfs is read once at native process start, not on each token.
// It does not change affinity or interfere with an explicit thread setting.
func RecommendedThreads(root string, logical int) int {
	if logical < 1 {
		return 1
	}
	fallback := logical
	if fallback > 4 {
		fallback = 4
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		return fallback
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fallback
	}
	values := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "cpu") || len(name) <= 3 {
			continue
		}
		if _, err := strconv.Atoi(name[3:]); err != nil {
			continue
		}
		dir := filepath.Join(root, name)
		if raw, err := os.ReadFile(filepath.Join(dir, "online")); err == nil && strings.TrimSpace(string(raw)) == "0" {
			continue
		}
		var capacity uint64
		for _, path := range []string{"cpu_capacity", "cpufreq/cpuinfo_max_freq"} {
			raw, err := os.ReadFile(filepath.Join(dir, path))
			if err != nil {
				continue
			}
			capacity, err = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
			if err == nil && capacity > 0 {
				break
			}
		}
		if capacity > 0 {
			values = append(values, capacity)
		}
	}
	// Missing per-core telemetry cannot reliably describe a topology.
	if len(values) < 2 || len(values) < logical {
		return fallback
	}
	var high, low uint64
	for _, n := range values {
		if n > high {
			high = n
		}
		if low == 0 || n < low {
			low = n
		}
	}
	if high == 0 || low >= high*80/100 {
		return fallback
	}
	count := 0
	for _, n := range values {
		if n >= high*80/100 {
			count++
		}
	}
	if count > logical {
		count = logical
	}
	if count == 0 {
		return fallback
	}
	return count
}

// Threads preserves existing behavior unless explicitly opted in via the
// NODEXLM_SMOLMAX_THREADS=performance environment variable.
func Threads(requested int, policy string) (int, error) {
	switch policy {
	case "":
		if requested > 0 {
			return requested, nil
		}
		return 4, nil
	case "performance":
		return RecommendedThreads("/sys/devices/system/cpu", runtime.NumCPU()), nil
	default:
		return 0, errors.New("NODEXLM_SMOLMAX_THREADS must be performance or unset")
	}
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package hardware

import (
	"sync"
	"time"
)

var cpuSample struct {
	sync.Mutex
	total uint64
	idle  uint64
	value *float64
	at    time.Time
}

// CPU usage is system-wide time spent not idle between two counter reads.
// A first reading (or inaccessible counters) is unknown, not a fabricated 0%.
func sampleCPU() *float64 {
	cpuSample.Lock()
	defer cpuSample.Unlock()
	if time.Since(cpuSample.at) < time.Second {
		return cpuSample.value
	}
	total, idle, ok := cpuTimes()
	if !ok {
		cpuSample.value = nil
		return nil
	}
	if cpuSample.total > 0 && total > cpuSample.total && idle >= cpuSample.idle {
		delta := total - cpuSample.total
		busy := delta - min(delta, idle-cpuSample.idle)
		value := 100 * float64(busy) / float64(delta)
		cpuSample.value = &value
	} else {
		cpuSample.value = nil
	}
	cpuSample.total, cpuSample.idle, cpuSample.at = total, idle, time.Now()
	return cpuSample.value
}

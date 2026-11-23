// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package hardware

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPhysicalCPUCountFromSysfs(t *testing.T) {
	root := t.TempDir()
	// 4 physical cores, 2 threads per core = 8 logical CPUs.
	for cpu := 0; cpu < 8; cpu++ {
		topo := filepath.Join(root, fmt.Sprintf("cpu%d", cpu), "topology")
		if err := os.MkdirAll(topo, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(topo, "physical_package_id"), []byte("0\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(topo, "core_id"), []byte(fmt.Sprintf("%d\n", cpu%4)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Multi-socket: same core_id on package 1 counts as distinct physical core.
	topo := filepath.Join(root, "cpu8", "topology")
	if err := os.MkdirAll(topo, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(topo, "physical_package_id"), []byte("1\n"), 0644)
	_ = os.WriteFile(filepath.Join(topo, "core_id"), []byte("0\n"), 0644)

	if got := physicalCPUCountFrom(root); got != 5 {
		t.Fatalf("got %d physical cores, want 5", got)
	}
	if got := physicalCPUCountFrom(t.TempDir()); got != 0 {
		t.Fatalf("empty sysfs should return 0, got %d", got)
	}
}

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

func writeFakeCPU(t *testing.T, root string, cpu, pkg, core int, capacity, maxFreq string) {
	t.Helper()
	cpuDir := filepath.Join(root, fmt.Sprintf("cpu%d", cpu))
	topo := filepath.Join(cpuDir, "topology")
	if err := os.MkdirAll(topo, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(topo, "physical_package_id"), []byte(fmt.Sprintf("%d\n", pkg)), 0644)
	_ = os.WriteFile(filepath.Join(topo, "core_id"), []byte(fmt.Sprintf("%d\n", core)), 0644)
	if capacity != "" {
		_ = os.WriteFile(filepath.Join(cpuDir, "cpu_capacity"), []byte(capacity+"\n"), 0644)
	}
	if maxFreq != "" {
		cpufreq := filepath.Join(cpuDir, "cpufreq")
		_ = os.MkdirAll(cpufreq, 0755)
		_ = os.WriteFile(filepath.Join(cpufreq, "cpuinfo_max_freq"), []byte(maxFreq+"\n"), 0644)
	}
}

func TestPhysicalCPUCountFromSysfsARM64BigLittle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "system", "cpu")
	// 4 little cores (cap 221) + 3 medium/big cores (cap 996) + 1 prime core (cap 1024).
	for cpu := 0; cpu < 4; cpu++ {
		writeFakeCPU(t, root, cpu, 0, cpu, "221", "2200000")
	}
	for cpu := 4; cpu < 7; cpu++ {
		writeFakeCPU(t, root, cpu, 0, cpu, "996", "3200000")
	}
	writeFakeCPU(t, root, 7, 0, 7, "1024", "3350000")

	if got := physicalCPUCountFrom(root); got != 4 {
		t.Fatalf("ARM64 big.LITTLE got %d fast cores, want 4", got)
	}
}

func TestPhysicalCPUCountFromSysfsIntelHybrid(t *testing.T) {
	devicesDir := t.TempDir()
	root := filepath.Join(devicesDir, "system", "cpu")
	// 4 P-cores with SMT (cpus 0-7) + 4 E-cores (cpus 8-11).
	for cpu := 0; cpu < 8; cpu++ {
		writeFakeCPU(t, root, cpu, 0, cpu/2, "", "5200000")
	}
	for cpu := 8; cpu < 12; cpu++ {
		writeFakeCPU(t, root, cpu, 0, cpu, "", "3800000")
	}
	pCorePMU := filepath.Join(devicesDir, "cpu_core")
	if err := os.MkdirAll(pCorePMU, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(pCorePMU, "cpus"), []byte("0-7\n"), 0644)

	if got := physicalCPUCountFrom(root); got != 4 {
		t.Fatalf("Intel hybrid cpu_core got %d P-cores, want 4", got)
	}
}

func TestPhysicalCPUCountFromSysfsFreqHybridVsUniform(t *testing.T) {
	hybridRoot := filepath.Join(t.TempDir(), "system", "cpu")
	for cpu := 0; cpu < 4; cpu++ {
		writeFakeCPU(t, hybridRoot, cpu, 0, cpu, "", "5200000")
	}
	for cpu := 4; cpu < 12; cpu++ {
		writeFakeCPU(t, hybridRoot, cpu, 0, cpu, "", "3800000")
	}
	if got := physicalCPUCountFrom(hybridRoot); got != 4 {
		t.Fatalf("frequency-hybrid got %d P-cores, want 4", got)
	}

	uniformRoot := filepath.Join(t.TempDir(), "system", "cpu")
	for cpu := 0; cpu < 4; cpu++ {
		writeFakeCPU(t, uniformRoot, cpu, 0, cpu, "", "4800000")
	}
	for cpu := 4; cpu < 8; cpu++ {
		writeFakeCPU(t, uniformRoot, cpu, 0, cpu, "", "4500000")
	}
	if got := physicalCPUCountFrom(uniformRoot); got != 8 {
		t.Fatalf("uniform-ish cores (<15%% boost spread) got %d cores, want 8", got)
	}
}

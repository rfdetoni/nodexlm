//go:build linux

package hardware

import "testing"

func TestCPUCounters(t *testing.T) {
	total, idle, ok := parseLinuxCPU("cpu  40 2 20 130 8 0 0 0 0 0\n")
	if !ok || total != 200 || idle != 138 {
		t.Fatalf("CPU counters: total=%d idle=%d ok=%t", total, idle, ok)
	}
	_, _, ok = parseLinuxCPU("cpu broken")
	if ok {
		t.Fatal("accepted invalid CPU counters")
	}
}

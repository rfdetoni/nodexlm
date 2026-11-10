//go:build !linux && !darwin && !windows

package hardware

func cpuTimes() (uint64, uint64, bool) { return 0, 0, false }

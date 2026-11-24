//go:build !windows && !darwin

package hardware

func platformPhysicalCPUCount() int { return 0 }

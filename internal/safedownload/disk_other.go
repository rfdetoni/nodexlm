//go:build !linux && !darwin && !freebsd && !windows

package safedownload

// Disk probing is unavailable on these targets; writes still propagate ENOSPC.
func FreeBytes(path string) (uint64, error) { return ^uint64(0), nil }

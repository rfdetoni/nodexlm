//go:build windows

package hardware

import (
	"syscall"
	"unsafe"
)

type cpuFiletime struct { Low, High uint32 }
func (v cpuFiletime) ticks() uint64 { return uint64(v.High)<<32 | uint64(v.Low) }

var getSystemTimes = syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemTimes")

func cpuTimes() (uint64, uint64, bool) {
	var idle, kernel, user cpuFiletime
	status, _, _ := getSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)),
	)
	if status == 0 { return 0, 0, false }
	// Windows kernel time includes idle. User + kernel is the total.
	return kernel.ticks() + user.ticks(), idle.ticks(), true
}

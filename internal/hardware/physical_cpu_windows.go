//go:build windows

package hardware

import (
	"encoding/binary"
	"syscall"
	"unsafe"
)

var getLogicalProcessorInformationEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetLogicalProcessorInformationEx")

func platformPhysicalCPUCount() int {
	var length uint32
	getLogicalProcessorInformationEx.Call(0, 0, uintptr(unsafe.Pointer(&length)))
	if length < 8 || length > 1<<20 {
		return 0
	}
	buf := make([]byte, length)
	ok, _, _ := getLogicalProcessorInformationEx.Call(0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&length)))
	if ok == 0 || int(length) > len(buf) {
		return 0
	}
	var fastest int
	var highest byte
	for buf = buf[:length]; len(buf) > 0; {
		if len(buf) < 10 {
			return 0
		}
		n := int(binary.LittleEndian.Uint32(buf[4:8]))
		if n < 10 || n > len(buf) || binary.LittleEndian.Uint32(buf[:4]) != 0 {
			return 0
		}
		class := buf[9]
		if class > highest {
			highest, fastest = class, 1
		} else if class == highest {
			fastest++
		}
		buf = buf[n:]
	}
	return fastest
}

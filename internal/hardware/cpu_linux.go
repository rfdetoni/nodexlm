//go:build linux

package hardware

import (
	"os"
	"strconv"
	"strings"
)

func cpuTimes() (uint64, uint64, bool) {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil { return 0, 0, false }
	return parseLinuxCPU(string(raw))
}

func parseLinuxCPU(raw string) (uint64, uint64, bool) {
	fields := strings.Fields(strings.SplitN(raw, "\n", 2)[0])
	if len(fields) < 5 || fields[0] != "cpu" { return 0, 0, false }
	var total, idle uint64
	for i, part := range fields[1:] {
		if i >= 8 { break } // guest time is already included in user/nice
		v, err := strconv.ParseUint(part, 10, 64)
		if err != nil { return 0, 0, false }
		total += v
		if i == 3 || i == 4 { idle += v }
	}
	return total, idle, total > 0
}

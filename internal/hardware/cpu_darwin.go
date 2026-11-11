//go:build darwin

package hardware

import (
	"strconv"
	"strings"
	"time"
)

func cpuTimes() (uint64, uint64, bool) {
	fields := strings.Fields(command(time.Second, "sysctl", "-n", "kern.cp_time"))
	if len(fields) != 5 {
		return 0, 0, false
	}
	var total, idle uint64
	for i, word := range fields {
		value, err := strconv.ParseUint(word, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += value
		if i == 3 {
			idle = value
		}
	}
	return total, idle, total > 0
}

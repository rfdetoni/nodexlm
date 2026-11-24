//go:build darwin

package hardware

import (
	"strconv"
	"strings"
	"time"
)

func platformPhysicalCPUCount() int {
	for _, key := range []string{"hw.perflevel0.physicalcpu", "hw.physicalcpu"} {
		n, err := strconv.Atoi(strings.TrimSpace(command(time.Second, "sysctl", "-n", key)))
		if err == nil && n > 0 {
			return n
		}
	}
	return 0
}

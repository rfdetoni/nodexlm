package main

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"time"
)

// Android may kill the foreground-service process without onDestroy.
// Stop the bundled coordinator if its actual parent disappears.
func watchAndroidParent(ctx context.Context, cancel context.CancelFunc) {
	if runtime.GOOS != "android" {
		return
	}
	parent, err := strconv.Atoi(os.Getenv("NODEXLM_ANDROID_PARENT_PID"))
	if err != nil || parent <= 1 {
		return
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			if os.Getppid() != parent {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

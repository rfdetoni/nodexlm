package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const guestUpdateReadyEnv = "NODEXLM_UPDATE_READY_FILE"

var guestUpdateReadyOnce sync.Once

func markGuestUpdateReady() {
	path := os.Getenv(guestUpdateReadyEnv)
	if !filepath.IsAbs(path) || filepath.Base(path) != "ready" ||
		!strings.HasPrefix(filepath.Base(filepath.Dir(path)), ".nodexlm-update-") {
		return
	}
	guestUpdateReadyOnce.Do(func() {
		// A private staging directory is created by the updating parent.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return
		}
		_, _ = f.WriteString(buildCommit)
		_ = f.Close()
	})
}

func guestUpdateConfirmed(path, commit string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(commit)) {
		return false
	}
	raw, err := os.ReadFile(path)
	return err == nil && string(raw) == commit
}

// The bools distinguish confirmation from whether the Wait result has been
// consumed. The parent is always responsible for reaping the child exactly once.
func awaitGuestUpdate(ctx context.Context, ready, commit string, exited <-chan error) (bool, error, bool) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(75 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case err := <-exited:
			return guestUpdateConfirmed(ready, commit), err, true
		case <-ctx.Done():
			return false, ctx.Err(), false
		case <-deadline.C:
			return false, errors.New("Guest reconnection confirmation timed out"), false
		case <-tick.C:
			if guestUpdateConfirmed(ready, commit) {
				return true, nil, false
			}
		}
	}
}

//go:build !linux && !darwin && !freebsd && !windows

package safedownload

import (
	"errors"
	"os"
)

func lockFile(f *os.File) error {
	return errors.New("model transfer locking unsupported on this platform")
}
func unlockFile(f *os.File) {}

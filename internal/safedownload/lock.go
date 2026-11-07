package safedownload

import (
	"errors"
	"os"
)

// LockTransfer protects stable staging files across separate CLI/Host
// processes. The advisory lock is released by the OS after a crash; retaining
// the lock inode avoids unlink-and-recreate races during publication.
func LockTransfer(path string) (func(), error) {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("transfer lock must be a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, errors.New("another process is transferring this model: " + err.Error())
	}
	return func() { unlockFile(f); _ = f.Close() }, nil
}

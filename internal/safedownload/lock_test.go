package safedownload

import (
	"path/filepath"
	"testing"
)

func TestTransferLockPreventsConcurrentPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.lock")
	release, err := LockTransfer(path)
	if err != nil {
		t.Fatal(err)
	}
	if another, err := LockTransfer(path); err == nil {
		another()
		release()
		t.Fatal("second writer accepted")
	}
	release()
	again, err := LockTransfer(path)
	if err != nil {
		t.Fatal(err)
	}
	again()
}

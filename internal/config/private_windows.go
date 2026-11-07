// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
//go:build windows

package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// NTFS ACLs, not synthetic os.FileMode bits, protect credentials on Windows.
// Grant only the current account access and remove inherited permissions.
func securePrivateACL(path string, directory bool) error {
	account, err := user.Current()
	if err != nil {
		return fmt.Errorf("lookup Windows account: %w", err)
	}
	if !strings.HasPrefix(account.Uid, "S-1-") {
		return errors.New("Windows account SID unavailable")
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		return errors.New("SystemRoot is not set")
	}
	executable := filepath.Join(root, "System32", "icacls.exe")
	permissions := "*" + account.Uid + ":(F)"
	if directory {
		permissions = "*" + account.Uid + ":(OI)(CI)(F)"
	}
	cmd := exec.Command(executable, path, "/grant:r", permissions, "/inheritance:r")
	result, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restrict Windows ACL for %s: %w: %s", path, err, strings.TrimSpace(string(result)))
	}
	return nil
}
func securePrivateFile(path string) error { return securePrivateACL(path, false) }
func securePrivateDir(path string) error  { return securePrivateACL(path, true) }

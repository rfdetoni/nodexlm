// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
//go:build !windows

package config

import (
	"errors"
	"os"
)

// Reject symlinks and loose Unix permissions for files containing node tokens.
func securePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("credential file must be regular and private (0600)")
	}
	return nil
}

// MkdirAll does not fix permissions on pre-existing directories. Refuse
// insecure folders rather than chmod an arbitrary user-configured path.
func securePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("credential directory must be a real directory")
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("credential directory must be private (0700); fix permissions before starting NodexLM")
	}
	return nil
}

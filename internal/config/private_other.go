// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
//go:build !windows

package config

import (
	"errors"
	"os"
)

func securePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("credential file must be regular and readable only by its owner")
	}
	return nil
}
func securePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("configuration path must be a real directory")
	}
	// Do not chmod an arbitrary configured directory: NODEXLM_HOME may
	// point to an existing shared tree. The operator must explicitly
	// restrict that directory before secrets may be written.
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("configuration directory must be private (0700); fix its permissions before starting NodexLM")
	}
	return nil
}

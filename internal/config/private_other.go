// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
//go:build !windows

package config

// POSIX permissions are enforced by file creation mode and ReadSecrets.
func securePrivateFile(string) error { return nil }
func securePrivateDir(string) error  { return nil }

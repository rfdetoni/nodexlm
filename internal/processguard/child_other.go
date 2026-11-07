//go:build !linux

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package processguard

import "os/exec"

// Job Objects on Windows and process supervision on macOS require separate
// implementation; explicit shutdown still terminates these children.
func Attach(cmd *exec.Cmd) { _ = cmd }

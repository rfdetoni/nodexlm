//go:build linux

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package processguard

import (
	"os/exec"
	"syscall"
)

// The native inference processes must not survive abrupt Host/Guest death.
func Attach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
//go:build linux

package process

import (
	"os/exec"
	"syscall"
)

// KillWithParent prevents abandoned GGML workers from retaining RAM/ports
// after a sudden NodexLM crash on Linux.
func KillWithParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

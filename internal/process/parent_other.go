// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
//go:build !linux

package process

import "os/exec"

// Other operating systems require a different supervisor mechanism.
func KillWithParent(_ *exec.Cmd) {}

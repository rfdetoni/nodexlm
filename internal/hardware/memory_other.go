//go:build !windows

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package hardware

func memoryWindows() (uint64, uint64) { return 0, 0 }

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// parseRPCExclusions deliberately requires complete IDs, not prefixes.
// Silent prefix matching could remove an unintended node and disguise a
// capacity-planning failure as a runtime problem.
func parseRPCExclusions(raw string) (map[string]struct{}, error) {
	excluded := make(map[string]struct{})
	if strings.TrimSpace(raw) == "" {
		return excluded, nil
	}
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if len(id) != 64 {
			return nil, fmt.Errorf("rpc-exclude-ids requires complete 64-character Guest IDs")
		}
		if _, err := hex.DecodeString(id); err != nil || id != strings.ToLower(id) {
			return nil, fmt.Errorf("rpc-exclude-ids requires lowercase hexadecimal Guest IDs")
		}
		excluded[id] = struct{}{}
	}
	return excluded, nil
}

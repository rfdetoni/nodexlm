// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/config"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestRejoinRejectsInvalidCodeWithoutReplacingSavedAgent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".nodexlm")
	original := config.Agent{
		Coordinator: "https://192.168.1.1:8443",
		Node: protocol.Credential{
			NodeID: strings.Repeat("a", 64),
			Token:  strings.Repeat("b", 64),
		},
	}
	if err := config.SaveAgent(dir, original); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := join([]string{"--dir", dir, "--code", "INVALID"}); err == nil || !strings.Contains(err.Error(), "already enrolled") {
		t.Fatalf("joining without explicit rejoin should refuse overwrite: %v", err)
	}
	if err := join([]string{"--rejoin", "--dir", dir, "--code", "INVALID"}); err == nil || !strings.Contains(err.Error(), "--code") {
		t.Fatalf("invalid rejoin code should be rejected before enrollment: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed rejoin changed saved credentials")
	}
}

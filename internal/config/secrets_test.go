// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureSecretsPersistsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	first, err := EnsureSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureSecrets(dir)
	if err != nil {
		t.Fatalf("reading existing coordinator secrets on restart: %v", err)
	}
	if first != second || len(first.Admin) != 64 || len(first.Join) != 64 || first.Admin == first.Join {
		t.Fatal("restarted coordinator did not reuse valid persisted credentials")
	}
}

func TestReadSecretsRejectsLooseUnixPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NTFS ACL permissions are not represented by Unix mode bits")
	}
	dir := t.TempDir()
	if _, err := EnsureSecrets(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "secrets.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSecrets(dir); err == nil {
		t.Fatal("accepted secrets readable by other local users")
	}
}

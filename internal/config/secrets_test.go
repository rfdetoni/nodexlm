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

func TestAgentCredentialsStayPrivate(t *testing.T) {
	dir := t.TempDir()
	first := Agent{Coordinator: "https://127.0.0.1:8443"}
	first.Node.NodeID = "node"
	first.Node.Token = "secret"
	if err := SaveAgent(dir, first); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadAgent(dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Node.NodeID != first.Node.NodeID || restored.Node.Token != first.Node.Token {
		t.Fatalf("agent credentials were not persisted correctly")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "agent.json"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatal("agent credentials are accessible by other users")
		}
	}
}

func TestUnsafeExistingDirectoryIsNotChmodded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows credentials are protected with NTFS ACLs")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := securePrivateDir(dir); err == nil {
		t.Fatal("accepted insecure pre-existing credentials directory")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("modified pre-existing directory permissions unexpectedly: %v", info.Mode().Perm())
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := securePrivateDir(dir); err != nil {
		t.Fatalf("rejected valid private configuration directory: %v", err)
	}
}

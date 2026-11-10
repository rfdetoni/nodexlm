// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package debuglog

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStartWritesPrivateHostAndNativeLogs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nodexlm")
	stop, err := Start(dir, "guest")
	if err != nil {
		t.Fatal(err)
	}
	slog.Debug("guest connected", "rpc", "ready")
	if _, err := NativeOutput().Write([]byte("native device CPU ready\n")); err != nil {
		t.Fatal(err)
	}
	stop()
	path := filepath.Join(dir, "logs", "guest.log")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "guest connected") || !strings.Contains(string(body), "native device CPU ready") {
		t.Fatalf("missing diagnostics: %s", body)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("log file permissions: %04o", info.Mode().Perm())
		}
	}
}

func TestStartRejectsUnsafeDirectoryAndSymlink(t *testing.T) {
	if _, err := Start(t.TempDir(), "invalid"); err == nil {
		t.Fatal("accepted unknown diagnostic log role")
	}
	if runtime.GOOS == "windows" {
		return
	}
	dir := filepath.Join(t.TempDir(), "nodexlm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(dir, "host"); err == nil {
		t.Fatal("accepted publicly readable diagnostic directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external")
	if err := os.MkdirAll(external, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dir, "logs")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := Start(dir, "host"); err == nil {
		t.Fatal("accepted symlinked diagnostic directory")
	}
}

func TestRotationBoundsFileAndPreservesBackup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nodexlm")
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(logs, "host.log")
	file, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxLogBytes-1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	stop, err := Start(dir, "host")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	info, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() >= maxLogBytes {
		t.Fatalf("debug segment not rotated: %d", info.Size())
	}
	if _, err := os.Stat(log + ".1"); err != nil {
		t.Fatal(err)
	}
}

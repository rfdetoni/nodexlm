// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/netclient"
)

func TestNativeRPCEnvironmentIncludesBundledLibraries(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux dynamic library lookup")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "bin", "ggml-rpc-server")
	t.Setenv("LD_LIBRARY_PATH", "/opt/example")
	got := nativeRPCEnvironment(p)
	var value string
	for _, entry := range got {
		if strings.HasPrefix(entry, "LD_LIBRARY_PATH=") {
			value = strings.TrimPrefix(entry, "LD_LIBRARY_PATH=")
		}
	}
	if value != filepath.Join(dir, "bin")+string(os.PathListSeparator)+"/opt/example" {
		t.Fatalf("RPC sibling libraries must precede system path: %q", value)
	}
}

func TestNativeRPCExitDoesNotLeaveTunnelsRunning(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("requires a POSIX executable")
	}
	executable := filepath.Join(t.TempDir(), "rpc-exit")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 19\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := RunNativeGuest(ctx, &netclient.Client{}, executable, "127.0.0.1:0", 1, false)
	if err == nil || !strings.Contains(err.Error(), "native RPC server exited before readiness") {
		t.Fatalf("expected prompt native RPC process-exit error; got %v", err)
	}
}

func TestNativeRPCRejectsAlreadyOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	err = RunNativeGuest(context.Background(), nil, "nonexistent-rpc", listener.Addr().String(), 1, false)
	if err == nil || !strings.Contains(err.Error(), "native RPC listen address unavailable") {
		t.Fatalf("expected occupied RPC port to be rejected; got %v", err)
	}
}

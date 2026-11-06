// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/netclient"
)

// RunNativeGuest supervises one RPC compute server and its TLS reverse tunnels.
// The model file is not required on this node: tensors are placed by the Host.
func RunNativeGuest(ctx context.Context, client *netclient.Client, executable, listen string, slots int) error {
	if err := ValidateRPCAddress(listen); err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	if host == "" {
		return errors.New("missing RPC listen host")
	}
	if _, err = strconv.Atoi(port); err != nil {
		return err
	}
	// Official ggml-rpc-server accepts --host and --port. Native server is not
	// reachable from other LAN devices; they only see the secured coordinator.
	cmd := exec.CommandContext(ctx, executable, "--host", host, "--port", port)
	cmd.Env = nativeRPCEnvironment(executable)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start native RPC server: %w", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ready := false
	for i := 0; i < 40; i++ {
		conn, e := net.DialTimeout("tcp", listen, 250*time.Millisecond)
		if e == nil {
			conn.Close()
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if !ready {
		return errors.New("native RPC server did not start")
	}
	slog.Info("NodexLM RPC worker ready", "listen", listen, "tunnels", slots)
	var wg sync.WaitGroup
	for i := 0; i < slots; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			delay := time.Second
			for ctx.Err() == nil {
				if err := Connect(ctx, client, listen); err != nil && ctx.Err() == nil {
					// An ALPN mismatch or broken relay can appear as EOF; do
					// not hide the reason when a Guest is online but has no RPC.
					slog.Warn("RPC tunnel disconnected", "slot", slot, "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
				if delay < 10*time.Second {
					delay *= 2
				}
			}
		}(i)
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

// nativeRPCEnvironment makes the bundled GGML shared libraries visible to the
// dynamic loader without requiring global installation or sudo.
func nativeRPCEnvironment(executable string) []string {
    env := os.Environ()
    dir := filepath.Dir(executable)
    switch runtime.GOOS {
    case "linux":
        value := dir
        if inherited := os.Getenv("LD_LIBRARY_PATH"); inherited != "" {
            value += string(os.PathListSeparator) + inherited
        }
        return append(env, "LD_LIBRARY_PATH="+value)
    case "darwin":
        value := dir
        if inherited := os.Getenv("DYLD_LIBRARY_PATH"); inherited != "" {
            value += string(os.PathListSeparator) + inherited
        }
        return append(env, "DYLD_LIBRARY_PATH="+value)
    }
    return env
}

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
	"strings"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/netclient"
	"github.com/rfdetoni/nodexlm/internal/process"
)

// RunNativeGuest supervises one RPC compute server and its TLS reverse tunnels.
// The model file is not required on this node: tensors are placed by the Host.
func RunNativeGuest(ctx context.Context, client *netclient.Client, executable, listen string, slots int, devices ...string) error {
	device := ""
	if len(devices) > 0 {
		device = devices[0]
	}
	if listen == "auto" {
		listen = "127.0.0.1:0"
		if runtime.GOOS != "windows" && llamaruntime.SupportsUnixRPC(ctx, executable) {
			dir, err := os.MkdirTemp("", "nodexlm-guest-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(dir)
			listen = "unix:" + filepath.Join(dir, "rpc.sock")
		}
	}
	if strings.HasPrefix(listen, "unix:") {
		return runUnixGuest(ctx, client, executable, listen, slots, device)
	}
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
	// Do not mistake an older RPC process already bound to this port for
	// the worker being launched; its protocol version could be incompatible.
	probe, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("native RPC listen address unavailable: %w", err)
	}
	listen = probe.Addr().String()
	_, port, _ = net.SplitHostPort(listen)
	_ = probe.Close()
	// The native GGML RPC server is always restricted to loopback.
	args := []string{"--host", host, "--port", port}
	if device != "" {
		args = append(args, "--device", device)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	process.KillWithParent(cmd)
	cmd.Env = nativeRPCEnvironment(executable)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start native RPC server: %w", err)
	}
	processExited := make(chan struct{})
	var processErr error
	go func() {
		processErr = cmd.Wait()
		close(processExited)
	}()
	defer func() {
		_ = cmd.Process.Kill()
		<-processExited
	}()

	ready := false
	// Match Unix readiness: cold GPU shader initialization can be slow.
	for i := 0; i < 360; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-processExited:
			return fmt.Errorf("native RPC server exited before readiness: %v", processErr)
		default:
		}
		conn, dialErr := net.DialTimeout("tcp", listen, 250*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-processExited:
			return fmt.Errorf("native RPC server exited before readiness: %v", processErr)
		case <-time.After(250 * time.Millisecond):
		}
	}
	if !ready {
		return errors.New("native RPC server did not start")
	}
	slog.Info("NodexLM RPC worker ready", "listen", listen, "tunnels", slots)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < slots; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			delay := time.Second
			for runCtx.Err() == nil {
				err := Connect(runCtx, client, listen)
				if err == nil {
					// A completed RPC session must not inherit a previous failure backoff.
					delay = time.Second
				} else if runCtx.Err() == nil {
					client.ReconnectFailures.Add(1)
					// Keep unexpected EOF visible: it may indicate a broken relay
					// or an incompatible native RPC session on the Host.
					slog.Warn("RPC tunnel disconnected", "slot", slot, "error", err)
				}
				if runCtx.Err() != nil {
					return
				}
				select {
				case <-runCtx.Done():
					return
				case <-time.After(delay):
				}
				if err != nil && delay < 10*time.Second {
					delay *= 2
					if delay > 10*time.Second {
						delay = 10 * time.Second
					}
				}
			}
		}(i)
	}
	select {
	case <-ctx.Done():
		cancel()
		wg.Wait()
		return nil
	case <-processExited:
		cancel()
		wg.Wait()
		if processErr != nil {
			return fmt.Errorf("native RPC server exited unexpectedly: %w", processErr)
		}
		return errors.New("native RPC server exited unexpectedly")
	}
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

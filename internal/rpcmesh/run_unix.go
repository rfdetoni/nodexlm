package rpcmesh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/debuglog"
	"github.com/rfdetoni/nodexlm/internal/netclient"
	"github.com/rfdetoni/nodexlm/internal/process"
)

func runUnixGuest(ctx context.Context, client *netclient.Client, executable, address string, slots int, device string, cache bool) error {
	if err := ValidateRPCAddress(address); err != nil {
		return err
	}
	args := []string{"--socket", strings.TrimPrefix(address, "unix:")}
	if cache {
		args = append(args, "--cache")
	}
	if device != "" {
		args = append(args, "--device", device)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	process.KillWithParent(cmd)
	cmd.Env = nativeRPCEnvironment(executable)
	cmd.Stdout = debuglog.NativeOutput()
	cmd.Stderr = debuglog.NativeOutput()
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan struct{})
	var processErr error
	go func() { processErr = cmd.Wait(); close(exited) }()
	defer func() {
		_ = cmd.Process.Kill()
		<-exited
	}()
	ready := false
	// A cold Metal shader library may take tens of seconds to compile.
	for range 900 {
		conn, err := net.DialTimeout("unix", strings.TrimPrefix(address, "unix:"), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			ready = true
			break
		}
		select {
		case <-exited:
			return fmt.Errorf("native Unix RPC exited: %v", processErr)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		return fmt.Errorf("native Unix RPC did not become ready")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	fatal := make(chan error, 1)
	var wg sync.WaitGroup
	for range slots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			delay := time.Second
			for runCtx.Err() == nil {
				err := Connect(runCtx, client, address)
				if errors.Is(err, ErrGuestCredentialsRejected) {
					select {
					case fatal <- err:
					default:
					}
					return
				}
				if err != nil && runCtx.Err() == nil {
					client.ReconnectFailures.Add(1)
					slog.Warn("RPC Unix tunnel disconnected", "error", err)
				} else if err == nil {
					delay = time.Second
				}
				if runCtx.Err() != nil {
					return
				}
				select {
				case <-runCtx.Done():
					return
				case <-time.After(jitteredRPCBackoff(delay)):
				}
				delay = nextRPCBackoff(delay, err)
			}
		}()
	}
	select {
	case <-ctx.Done():
		cancel()
		wg.Wait()
		return nil
	case err := <-fatal:
		cancel()
		wg.Wait()
		return fmt.Errorf("RPC Guest must be re-paired: %w", err)
	case <-exited:
		cancel()
		wg.Wait()
		return fmt.Errorf("native Unix RPC stopped: %v", processErr)
	}
}

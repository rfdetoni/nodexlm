package rpcmesh

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/netclient"
	"github.com/rfdetoni/nodexlm/internal/process"
)

func runUnixGuest(ctx context.Context, client *netclient.Client, executable, address string, slots int) error {
	if err := ValidateRPCAddress(address); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable, "--socket", strings.TrimPrefix(address, "unix:"))
	process.KillWithParent(cmd)
	cmd.Env = nativeRPCEnvironment(executable)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
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
	var wg sync.WaitGroup
	for range slots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for runCtx.Err() == nil {
				if err := Connect(runCtx, client, address); err != nil && runCtx.Err() == nil {
					client.ReconnectFailures.Add(1)
				}
				select {
				case <-runCtx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		}()
	}
	select {
	case <-ctx.Done():
		cancel()
		wg.Wait()
		return nil
	case <-exited:
		cancel()
		wg.Wait()
		return fmt.Errorf("native Unix RPC stopped: %v", processErr)
	}
}

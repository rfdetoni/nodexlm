package rpcmesh

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
)

// This is a real native integration gate, not a simulated HELLO response.
// The Trellis candidate workflow supplies the already compiled worker.
func TestTrellisNativeCandidate(t *testing.T) {
	worker := os.Getenv("NODEXLM_TRELLIS_TEST_WORKER")
	if worker == "" {
		t.Skip("Trellis candidate executable not supplied")
	}
	if !filepath.IsAbs(worker) {
		t.Fatal("candidate executable path must be absolute")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := ln.Addr().String()
	_, port, err := net.SplitHostPort(endpoint)
	_ = ln.Close()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, worker, "--host", "127.0.0.1", "--port", port, "--device", "CPU", "--threads", "2")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	for {
		conn, err := net.DialTimeout("tcp", endpoint, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("candidate did not open loopback port")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if got, err := ProbeTrellisTensorUpload(ctx, endpoint, 0); err != nil || got != 1 {
		t.Fatalf("Trellis quantized preflight count=%d err=%v", got, err)
	}
	if _, err := ProbeHello(ctx, endpoint); err == nil {
		t.Fatal("stock probe accepted Trellis worker")
	}
	if err := llamaruntime.VerifyTrellisDevices(ctx, worker, "CPU"); err != nil {
		t.Fatalf("Guest native graph admission failed: %v", err)
	}
}

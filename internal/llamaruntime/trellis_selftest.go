package llamaruntime

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const trellisSelfTestBinary = "nodexlm-trellis-rpc-tensors"

//go:embed testdata/trellis-rpc-tensors.cpp
var trellisSelfTestSource []byte

func addTrellisSelfTest(src string) error {
	dir := filepath.Join(src, "tools", "rpc")
	if err := os.WriteFile(filepath.Join(dir, trellisSelfTestBinary+".cpp"), trellisSelfTestSource, 0600); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "CMakeLists.txt"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.WriteString("\nadd_executable(nodexlm-trellis-rpc-tensors nodexlm-trellis-rpc-tensors.cpp)\ntarget_link_libraries(nodexlm-trellis-rpc-tensors PRIVATE ggml ggml-rpc)\ntarget_compile_features(nodexlm-trellis-rpc-tensors PRIVATE cxx_std_17)\n")
	return errors.Join(err, f.Close())
}

// VerifyTrellisDevices runs the actual quantized compute gate on each
// candidate device before the Guest advertises its usable topology.
func VerifyTrellisDevices(ctx context.Context, worker, devices string) error {
	test := executable(filepath.Dir(worker), trellisSelfTestBinary)
	if test == "" {
		return errors.New("Trellis native graph test unavailable")
	}
	for _, device := range strings.Split(devices, ",") {
		if device == "" {
			return errors.New("empty Trellis device")
		}
		if err := verifyTrellisDevice(ctx, worker, test, device); err != nil {
			return fmt.Errorf("Trellis device %s: %w", device, err)
		}
	}
	return nil
}

func verifyTrellisDevice(ctx context.Context, worker, test, device string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	endpoint := ln.Addr().String()
	_, port, err := net.SplitHostPort(endpoint)
	_ = ln.Close()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, worker, "--host", "127.0.0.1", "--port", port, "--device", device, "--threads", "2")
	cmd.Env = NativeEnvironment(worker)
	// Keep native driver/compiler diagnostics visible without unbounded buffers.
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
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
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	check := exec.CommandContext(ctx, test, endpoint)
	check.Env = NativeEnvironment(test)
	check.Stdout, check.Stderr = os.Stderr, os.Stderr
	return check.Run()
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/config"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// Managed is a lazy local llama-server owner. It keeps GGUF tensors out of
// the resident NodexLM process and returns RAM to the OS after idle timeout.
// One active inference at a time in the alpha runtime.
type Managed struct {
	mu                       sync.Mutex
	Binary, ModelPath, Alias string
	Threads, ContextTokens   int
	GPULayers                string
	MoECPU                   bool
	MoECPULayers             int
	MoECacheMiB              int
	Idle                     time.Duration
	KeepLoaded               bool
	RPCServers               []string
	// RPCLocalLayers keeps the first N transformer blocks on the host CPU;
	// remaining blocks are placed on RPC devices using layer-split mode.
	RPCLocalLayers int
	started        *exec.Cmd
	endpoint       *Engine
	idleTimer      *time.Timer
}

// Load starts a resident llama-server only when explicitly requested.
func (m *Managed) Load(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensure(ctx)
}

func (m *Managed) Generate(ctx context.Context, request protocol.ChatRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	if err := m.ensure(ctx); err != nil {
		return "", err
	}
	text, err := m.endpoint.Generate(ctx, request)
	if m.KeepLoaded {
		// Explicitly loaded by the dashboard; only /unload releases its memory.
	} else if m.Idle <= 0 {
		m.stopLocked()
	} else {
		m.idleTimer = time.AfterFunc(m.Idle, func() { m.mu.Lock(); defer m.mu.Unlock(); m.stopLocked() })
	}
	return text, err
}
func (m *Managed) ensure(ctx context.Context) error {
	if m.started != nil {
		return nil
	}
	if m.Binary == "" || m.Binary == "llama-server" {
		executable, err := llamaruntime.AutoExecutable(ctx, config.DefaultDir(), m.Binary)
		if err != nil {
			return fmt.Errorf("llama.cpp automatic installation failed: %w", err)
		}
		m.Binary = executable
	}
	if m.ModelPath == "" {
		return errors.New("local model path required")
	}
	if !strings.EqualFold(filepath.Ext(m.ModelPath), ".gguf") {
		return errors.New("only GGUF model files are supported")
	}
	meta, err := InspectGGUF(m.ModelPath)
	if err != nil {
		return fmt.Errorf("invalid GGUF model: %w", err)
	}
	if err := m.validateMoE(meta); err != nil {
		return err
	}
	if len(m.RPCServers) > 0 {
		if meta.BlockCount == 0 {
			return errors.New("distributed model requires GGUF architecture.block_count metadata")
		}
		if m.RPCLocalLayers < 0 || uint32(m.RPCLocalLayers) > meta.BlockCount {
			return errors.New("invalid local-layer placement for distributed GGUF")
		}
		for _, addr := range m.RPCServers {
			host, _, err := net.SplitHostPort(addr)
			ip := net.ParseIP(host)
			if err != nil || ip == nil || !ip.IsLoopback() {
				return errors.New("RPC endpoints must be loopback-only authenticated relays")
			}
		}
	}
	bind, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := bind.Addr().(*net.TCPAddr).Port
	_ = bind.Close()
	threads := m.Threads
	if threads <= 0 {
		threads = 4
	}
	nctx := m.ContextTokens
	if nctx <= 0 {
		nctx = 2048
	}
	gpu := m.GPULayers
	if gpu == "" {
		gpu = "auto"
	}
	args := m.args(port, nctx, threads, gpu, meta.BlockCount)
	cmd := exec.Command(m.Binary, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch llama-server: %w", err)
	}
	endpoint, err := New("http://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	startup, stop := context.WithTimeout(ctx, 90*time.Second)
	defer stop()
	for {
		req, err := http.NewRequestWithContext(startup, "GET", endpoint.BackendURL+"/health", nil)
		if err == nil {
			res, err := endpoint.HTTP.Do(req)
			if err == nil {
				res.Body.Close()
				if res.StatusCode == 200 {
					m.started = cmd
					m.endpoint = endpoint
					return nil
				}
			}
		}
		if startup.Err() != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return errors.New("llama-server did not become ready before deadline")
		}
		select {
		case <-startup.Done():
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return errors.New("llama-server startup cancelled")
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func (m *Managed) stopLocked() {
	if m.started != nil {
		_ = m.started.Process.Kill()
		_ = m.started.Wait()
		m.started = nil
		m.endpoint = nil
	}
}
func (m *Managed) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idleTimer != nil {
		m.idleTimer.Stop()
	}
	m.stopLocked()
}

func (m *Managed) validateMoE(meta GGUFInfo) error {
	if m.MoECPULayers < 0 || m.MoECacheMiB < 0 {
		return errors.New("MoE layer count and cache must be nonnegative")
	}
	if m.MoECPU && m.MoECPULayers > 0 {
		return errors.New("--moe-cpu and --moe-cpu-layers are mutually exclusive")
	}
	if m.MoECacheMiB > 0 && !m.MoECPU && m.MoECPULayers == 0 {
		return errors.New("MoE GPU cache requires --moe-cpu or --moe-cpu-layers")
	}
	if (m.MoECPU || m.MoECPULayers > 0 || m.MoECacheMiB > 0) && !meta.IsMoE {
		return errors.New("MoE options require a GGUF model with expert_count metadata")
	}
	return nil
}

func (m *Managed) args(port, nctx, threads int, gpu string, blocks uint32) []string {
	args := []string{"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--model", m.ModelPath, "--alias", m.Alias, "--ctx-size", strconv.Itoa(nctx), "--threads", strconv.Itoa(threads), "--gpu-layers", gpu, "--no-webui"}
	switch {
	case m.MoECPU:
		args = append(args, "--cpu-moe")
	case m.MoECPULayers > 0:
		args = append(args, "--n-cpu-moe", strconv.Itoa(m.MoECPULayers))
	}
	if m.MoECacheMiB > 0 {
		args = append(args, "--moe-cache-mib", strconv.Itoa(m.MoECacheMiB))
	}
	if len(m.RPCServers) > 0 && blocks > 0 {
		// Only use authenticated loopback relay endpoints in the native process.
		// ensure() returns a hard error if one is invalid, too.
		for _, addr := range m.RPCServers {
			host, _, err := net.SplitHostPort(addr)
			ip := net.ParseIP(host)
			if err != nil || ip == nil || !ip.IsLoopback() {
				return args
			}
		}
		// llama.cpp offloads the LAST N blocks; the host CPU keeps the FIRST
		// RPCLocalLayers blocks. Include the output layer in the count because
		// it would otherwise be pushed onto a Guest by llama.cpp.
		offloaded := int(blocks) + 1 - m.RPCLocalLayers
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--gpu-layers" {
				args[i+1] = strconv.Itoa(offloaded)
				break
			}
		}
		args = append(args,
			"--rpc", strings.Join(m.RPCServers, ","),
			"--split-mode", "layer",
			"--override-tensor", `^(output|token_embd)\.weight$=CPU`,
			"--no-op-offload",
		)
	}

	return args
}

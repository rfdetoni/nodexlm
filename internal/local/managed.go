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
	"github.com/rfdetoni/nodexlm/internal/debuglog"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/process"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

// Managed is a lazy local llama-server owner. It keeps GGUF tensors out of
// the resident NodexLM process and returns RAM to the OS after idle timeout.
// One active inference at a time in the alpha runtime.
type Managed struct {
	mu                                           sync.Mutex
	Binary, ModelPath, Alias                     string
	Backend                                      string
	Threads, ContextTokens                       int
	GPULayers                                    string
	MoECPU                                       bool
	MoECPULayers                                 int
	MoECacheMiB                                  int
	FlashAttention, KVCacheType, SpeculativeMode string
	DraftModelPath                               string
	BatchSize, UBatchSize                        int
	Idle                                         time.Duration
	KeepLoaded                                   bool
	RPCServers                                   []string
	// RPCLocalLayers keeps the first N transformer blocks on the host CPU;
	// remaining blocks are placed on RPC devices using layer-split mode.
	RPCLocalLayers int
	started        *exec.Cmd
	processExit    *nativeProcessExit
	endpoint       *Engine
	idleTimer      *time.Timer
}

// nativeProcessExit ensures the child is reaped exactly once, even if it exits
// during initialization or the user cancels a long distributed model load.
type nativeProcessExit struct {
	done chan struct{}
	err  error
}

// nativeLogTail retains only the latest stderr bytes for actionable errors.
type nativeLogTail struct {
	mu   sync.Mutex
	data []byte
}

func (l *nativeLogTail) Write(p []byte) (int, error) {
	n := len(p)
	l.mu.Lock()
	defer l.mu.Unlock()
	const limit = 4096
	if len(p) >= limit {
		l.data = append(l.data[:0], p[len(p)-limit:]...)
		return n, nil
	}
	if excess := len(l.data) + len(p) - limit; excess > 0 {
		l.data = append([]byte(nil), l.data[excess:]...)
	}
	l.data = append(l.data, p...)
	return n, nil
}

func (l *nativeLogTail) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.TrimSpace(string(l.data))
}

// Load starts a resident llama-server only when explicitly requested.
func (m *Managed) Load(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensure(ctx)
}

func (m *Managed) Generate(ctx context.Context, request protocol.ChatRequest) (string, error) {

	var answer string
	m.normalizeModel(&request)
	err := m.withEndpoint(ctx, func(engine *Engine) error { var err error; answer, err = engine.Generate(ctx, request); return err })
	return answer, err
}
func (m *Managed) ensure(ctx context.Context) error {
	if m.started != nil {
		return nil
	}
	if EffectiveBackend(m.Backend)==BackendLlama && (m.Binary == "" || m.Binary == "llama-server") {
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
	if _, err := ModelSize(m.ModelPath); err != nil {
		return fmt.Errorf("missing GGUF shards: %w", err)
	}

	if m.SpeculativeMode == "draft" {
		if m.DraftModelPath == "" || m.DraftModelPath == m.ModelPath {
			return errors.New("draft speculative decoding requires another GGUF")
		}
		if _, err := ModelSize(m.DraftModelPath); err != nil {
			return fmt.Errorf("draft GGUF unavailable: %w", err)
		}
	}
	meta, err := InspectGGUF(m.ModelPath)
	if err != nil {
		return fmt.Errorf("invalid GGUF model: %w", err)
	}
	if EffectiveBackend(m.Backend)==BackendLlama {
		if err := m.validateMoE(meta); err != nil {return err}
	}
	if len(m.RPCServers) > 0 {
		if meta.BlockCount == 0 {
			return errors.New("distributed model requires GGUF architecture.block_count metadata")
		}
		if m.RPCLocalLayers < 0 || uint32(m.RPCLocalLayers) > meta.BlockCount {
			return errors.New("invalid local-layer placement for distributed GGUF")
		}
		for _, addr := range m.RPCServers {
			if err := rpcmesh.ValidateRPCAddress(addr); err != nil {
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
	launch, err := m.backendLaunch(port,nctx,threads,gpu,meta)
	if err!=nil {return err}
	cmd:=exec.Command(launch.Binary,launch.Args...)
	cmd.Env=append(os.Environ(),launch.Env...)
	process.KillWithParent(cmd)
	cmd.Stdout = io.Discard
	stderr := &nativeLogTail{}
	cmd.Stderr = io.MultiWriter(debuglog.NativeOutput(), stderr)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch %s: %w",EffectiveBackend(m.Backend),err)
	}
	exit := &nativeProcessExit{done: make(chan struct{})}
	go func() {
		exit.err = cmd.Wait()
		close(exit.done)
	}()
	stopProcess := func() {
		_ = cmd.Process.Kill()
		<-exit.done
	}
	endpoint, err := New("http://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		stopProcess()
		return err
	}
	endpoint.APIKey=launch.APIKey
	timeout:=launch.Timeout
	startup, stop := context.WithTimeout(ctx, timeout)
	defer stop()
	for {
		select {
		case <-exit.done:
			return fmt.Errorf("%s exited while loading the model: %v; recent stderr: %s",EffectiveBackend(m.Backend),exit.err,stderr.String())
		default:
		}
		req, err := http.NewRequestWithContext(startup, "GET", endpoint.BackendURL+launch.ReadyPath, nil)
		if err == nil {
			if launch.APIKey!="" {req.Header.Set("Authorization","Bearer "+launch.APIKey)}
			res, requestErr := endpoint.HTTP.Do(req)
			if requestErr == nil {
				ready:=backendIsReady(EffectiveBackend(m.Backend),res)
				res.Body.Close()
				if ready {
					select {
					case <-exit.done:
						return fmt.Errorf("%s exited before becoming ready: %v; recent stderr: %s",EffectiveBackend(m.Backend),exit.err,stderr.String())
					default:
					}
					m.started = cmd
					m.processExit = exit
					m.endpoint = endpoint
					return nil
				}
			}
		}
		select {
		case <-exit.done:
			return fmt.Errorf("%s exited while loading the model: %v; recent stderr: %s",EffectiveBackend(m.Backend),exit.err,stderr.String())
		case <-startup.Done():
			stopProcess()
			if errors.Is(startup.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%s not ready after %s; check model and stderr: %s",EffectiveBackend(m.Backend),timeout,stderr.String())
			}
			return fmt.Errorf("%s startup cancelled: %w",EffectiveBackend(m.Backend),startup.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func (m *Managed) stopLocked() {
	if m.started != nil {
		_ = m.started.Process.Kill()
		if m.processExit != nil {
			<-m.processExit.done
		}
		m.started = nil
		m.processExit = nil
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
	args := []string{"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--model", m.ModelPath, "--alias", m.Alias, "--ctx-size", strconv.Itoa(nctx), "--threads", strconv.Itoa(threads), "--gpu-layers", gpu, "--no-webui", "--jinja"}
	if m.FlashAttention != "" && m.FlashAttention != "auto" {
		args = append(args, "--flash-attn", m.FlashAttention)
	}
	if m.KVCacheType != "" && m.KVCacheType != "f16" {
		args = append(args, "--cache-type-k", m.KVCacheType, "--cache-type-v", m.KVCacheType)
	}
	if m.BatchSize > 0 {
		args = append(args, "--batch-size", strconv.Itoa(m.BatchSize))
	}
	if m.UBatchSize > 0 {
		args = append(args, "--ubatch-size", strconv.Itoa(m.UBatchSize))
	}

	if m.SpeculativeMode == "ngram" {
		args = append(args, "--spec-type", "ngram-simple")
	} else if m.SpeculativeMode == "draft" && m.DraftModelPath != "" {
		// Draft stays on Host CPU to avoid allocating another model on the
		// active RPC Guest backends. GGML validates vocabulary compatibility.
		args = append(args, "--spec-type", "draft-simple", "--spec-draft-model", m.DraftModelPath, "--spec-draft-ngl", "0")
	}
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
			if err := rpcmesh.ValidateRPCAddress(addr); err != nil {
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

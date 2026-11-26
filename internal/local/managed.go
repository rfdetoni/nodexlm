// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rfdetoni/nodexlm/internal/config"
	"github.com/rfdetoni/nodexlm/internal/debuglog"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/process"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
	"github.com/rfdetoni/nodexlm/internal/smolmax"
)

// Managed is a lazy local llama-server owner. It keeps GGUF tensors out of
// the resident NodexLM process and returns RAM to the OS after idle timeout.
// Resident runtimes can explicitly opt into bounded parallel slots.
type Managed struct {
	mu                                           sync.Mutex
	Binary, ModelPath, Alias                     string
	Backend                                      string
	Threads, ContextTokens                       int
	ThreadsBatch, PromptCacheMiB                 int
	ParallelSlots                                int
	LoadMode, LazyMode, NUMA                     string
	GPULayers                                    string
	MoECPU                                       bool
	MoECPULayers                                 int
	MoECacheMiB                                  int
	MoECacheCPUShare                             float64
	FlashAttention, KVCacheType, SpeculativeMode string
	DraftModelPath                               string
	VisionProjectorPath                          string
	BatchSize, UBatchSize                        int
	SpeculativeTokens, CacheReuseTokens          int
	Idle                                         time.Duration
	KeepLoaded                                   bool
	RPCServers                                   []string
	// Native RPC devices are selected by the Host before model loading.
	// RPCDeviceNames contains registered llama.cpp names such as RPC0/RPC1;
	// empty preserves upstream default behavior for legacy Guests.
	RPCDeviceNames []string
	RPCTensorSplit string
	// RPCLocalLayers keeps the first N transformer blocks on the host CPU;
	// remaining blocks are placed on RPC devices using layer-split mode.
	RPCLocalLayers int
	started        *exec.Cmd
	processExit    *nativeProcessExit
	endpoint       *Engine
	idleTimer      *time.Timer
	systemInfo     string
	nativeActivity atomic.Pointer[nativeTrace]
}

// nativeProcessExit ensures the child is reaped exactly once, even if it exits
// during initialization or the user cancels a long distributed model load.
type nativeProcessExit struct {
	done chan struct{}
	err  error
}

// nativeLogTail retains only the latest stderr bytes for actionable errors.
type nativeLogTail struct {
	mu         sync.Mutex
	data       []byte
	systemInfo string
}

func (l *nativeLogTail) Write(p []byte) (int, error) {
	n := len(p)
	l.mu.Lock()
	defer l.mu.Unlock()
	const limit = 4096
	if len(p) >= limit {
		l.data = append(l.data[:0], p[len(p)-limit:]...)
	} else {
		if excess := len(l.data) + len(p) - limit; excess > 0 {
			l.data = append([]byte(nil), l.data[excess:]...)
		}
		l.data = append(l.data, p...)
	}
	if l.systemInfo == "" {
		prefix := []byte("system_info: n_threads = ")
		if at := bytes.Index(l.data, prefix); at >= 0 {
			if end := bytes.IndexByte(l.data[at:], '\n'); end > len(prefix) && end <= 2048 {
				line := strings.TrimSpace(string(l.data[at : at+end]))
				if strings.IndexFunc(line, func(r rune) bool { return r < 32 || r > 126 }) < 0 {
					l.systemInfo = line
				}
			}
		}
	}
	return n, nil
}

func (l *nativeLogTail) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.TrimSpace(string(l.data))
}

func (l *nativeLogTail) SystemInfo() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.systemInfo
}

// SystemInfo is startup-reported native capability information, not guessed
// instruction usage. Empty means the native runtime did not report it.
func (m *Managed) SystemInfo() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.systemInfo
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
		// Wait() is owned by the single reaper goroutine. An exited server
		// cannot be reused even if its endpoint was previously healthy.
		if m.processExit != nil {
			select {
			case <-m.processExit.done:
				slog.Warn("Native inference process exited", "backend", EffectiveBackend(m.Backend),
					"error", m.processExit.err)
				m.started = nil
				m.processExit = nil
				m.endpoint = nil
			default:
				return nil
			}
		} else {
			// Do not expose a stale endpoint with no process lifecycle.
			m.started = nil
			m.endpoint = nil
		}
	}
	m.nativeActivity.Store(nil)
	if EffectiveBackend(m.Backend) == BackendLlama && (m.Binary == "" || m.Binary == "llama-server") {
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
	if EffectiveBackend(m.Backend) == BackendLlama {
		if err := m.validateMoE(meta); err != nil {
			return err
		}
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
	threads, err := smolmax.Threads(m.Threads, os.Getenv("NODEXLM_SMOLMAX_THREADS"))
	if err != nil {
		return err
	}
	nctx := m.ContextTokens
	if nctx <= 0 {
		nctx = 2048
	}
	gpu := m.GPULayers
	if gpu == "" {
		gpu = "auto"
	}
	launch, err := m.backendLaunch(port, nctx, threads, gpu, meta)
	if err != nil {
		return err
	}
	cmd := exec.Command(launch.Binary, launch.Args...)
	// Bundled llama.cpp binaries load libggml.so from their own directory.
	// Without this, startup may fail even when the --help RPC probe passed.
	if EffectiveBackend(m.Backend) == BackendLlama {
		smolEnv, err := smolmax.NativeEnvironment(launch.Binary, os.Getenv("NODEXLM_SMOLMAX_MODE"), meta.IsMoE, meta.ExpertCount, gpu, len(m.RPCServers))
		if err != nil {
			return fmt.Errorf("smolmax native settings: %w", err)
		}
		cmd.Env = append(llamaruntime.NativeEnvironment(launch.Binary), launch.Env...)
		cmd.Env = append(cmd.Env, smolEnv...)
		// A candidate patch is not evidence that the pinned release uses it.
		// Only verified bundles may automatically opt into parallel uploads.
		if m.parallelRPCUploadEligible() && llamaruntime.BundledParallelRPCUploadsSupported() &&
			os.Getenv("NODEXLM_PARALLEL_RPC_UPLOAD") == "" {
			cmd.Env = append(cmd.Env, "NODEXLM_PARALLEL_RPC_UPLOAD=1")
		}
	} else {
		if mode := os.Getenv("NODEXLM_SMOLMAX_MODE"); mode != "" && mode != "off" {
			return errors.New("smolmax requires the llama.cpp backend")
		}
		cmd.Env = append(os.Environ(), launch.Env...)
	}
	process.KillWithParent(cmd)
	// llama.cpp versions and GPU backends may log startup information to
	// stdout instead of stderr. Preserve both streams in the private debug
	// log and in the bounded diagnostic tail; do not lose the only evidence
	// of a stuck RPC HELLO or model load.
	stderr := &nativeLogTail{}
	cmd.Stdout = io.MultiWriter(debuglog.NativeOutput(), stderr)
	cmd.Stderr = io.MultiWriter(debuglog.NativeOutput(), stderr)
	var trace *nativeTrace
	if EffectiveBackend(m.Backend) == BackendLlama && os.Getenv("NODEXLM_NATIVE_TOKEN_TRACE") == "1" {
		trace = &nativeTrace{}
		cmd.Stderr = io.MultiWriter(cmd.Stderr, trace)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch %s: %w", EffectiveBackend(m.Backend), err)
	}
	slog.Info("Native inference process started", "backend", EffectiveBackend(m.Backend),
		"pid", cmd.Process.Pid, "rpc_guests", len(m.RPCServers), "model", filepath.Base(m.ModelPath))
	startedAt := time.Now()
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
	endpoint.APIKey = launch.APIKey
	timeout := launch.Timeout
	startup, stop := context.WithTimeout(ctx, timeout)
	defer stop()
	for {
		select {
		case <-exit.done:
			return fmt.Errorf("%s exited while loading the model: %v; recent native output: %s", EffectiveBackend(m.Backend), exit.err, stderr.String())
		default:
		}
		// Each /health probe has its own short deadline, independent of the
		// potentially long distributed model load. A hung health response
		// must not block the startup loop for minutes.
		ready := func() bool {
			checkCtx, stopCheck := context.WithTimeout(startup, 5*time.Second)
			defer stopCheck()
			req, reqErr := http.NewRequestWithContext(checkCtx, "GET", endpoint.BackendURL+launch.ReadyPath, nil)
			if reqErr != nil {
				return false
			}
			if launch.APIKey != "" {
				req.Header.Set("Authorization", "Bearer "+launch.APIKey)
			}
			res, requestErr := endpoint.HTTP.Do(req)
			if requestErr != nil {
				return false
			}
			defer res.Body.Close()
			return backendIsReady(EffectiveBackend(m.Backend), res)
		}()
		if ready {
			select {
			case <-exit.done:
				return fmt.Errorf("%s exited before becoming ready: %v; recent native output: %s", EffectiveBackend(m.Backend), exit.err, stderr.String())
			default:
			}
			m.started = cmd
			m.processExit = exit
			m.endpoint = endpoint
			m.systemInfo = stderr.SystemInfo()
			m.nativeActivity.Store(trace)
			slog.Info("Native inference model ready", "backend", EffectiveBackend(m.Backend),
				"elapsed_seconds", int(time.Since(startedAt).Seconds()), "rpc_guests", len(m.RPCServers))
			return nil
		}
		select {
		case <-exit.done:
			return fmt.Errorf("%s exited while loading the model: %v; recent native output: %s", EffectiveBackend(m.Backend), exit.err, stderr.String())
		case <-startup.Done():
			stopProcess()
			if errors.Is(startup.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%s not ready after %s; check model and native output: %s", EffectiveBackend(m.Backend), timeout, stderr.String())
			}
			return fmt.Errorf("%s startup cancelled: %w", EffectiveBackend(m.Backend), startup.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func (m *Managed) stopLocked() {
	m.nativeActivity.Store(nil)
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
	if m.MoECPULayers < 0 || m.MoECacheMiB < 0 || !(m.MoECacheCPUShare >= 0 && m.MoECacheCPUShare <= 1) {
		return errors.New("MoE layer count and cache must be nonnegative")
	}
	if m.MoECPU && m.MoECPULayers > 0 {
		return errors.New("--moe-cpu and --moe-cpu-layers are mutually exclusive")
	}
	if m.MoECacheMiB > 0 && !m.MoECPU && m.MoECPULayers == 0 {
		return errors.New("MoE GPU cache requires --moe-cpu or --moe-cpu-layers")
	}
	if m.MoECacheCPUShare > 0 && m.MoECacheMiB == 0 {
		return errors.New("MoE CPU miss share requires a GPU expert cache")
	}
	if (m.MoECPU || m.MoECPULayers > 0 || m.MoECacheMiB > 0 || m.MoECacheCPUShare > 0) && !meta.IsMoE {
		return errors.New("MoE options require a GGUF model with expert_count metadata")
	}
	return nil
}

func (m *Managed) args(port, nctx, threads int, gpu string, blocks uint32) []string {
	args := []string{"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--model", m.ModelPath, "--alias", m.Alias, "--ctx-size", strconv.Itoa(nctx), "--threads", strconv.Itoa(threads), "--gpu-layers", gpu, "--no-webui", "--jinja"}
	// Reserve context per request, not per process.
	if slots := max(1, m.ParallelSlots); slots > 1 {
		args = append(args, "--parallel", strconv.Itoa(slots), "--cont-batching", "--no-kvu")
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--ctx-size" {
				args[i+1] = strconv.Itoa(nctx * slots)
				break
			}
		}
	} else {
		args = append(args, "--parallel", "1")
	}
	args = append(args, "--cache-ram", strconv.Itoa(m.PromptCacheMiB))
	if m.ThreadsBatch > 0 {
		args = append(args, "--threads-batch", strconv.Itoa(m.ThreadsBatch))
	}
	if m.LoadMode != "" && m.LoadMode != "auto" {
		args = append(args, "--load-mode", m.LoadMode)
	}
	if m.LazyMode != "" && m.LazyMode != "auto" {
		args = append(args, "--lazy-mode", m.LazyMode)
	}
	if m.NUMA != "" {
		args = append(args, "--numa", m.NUMA)
	}
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
	if m.CacheReuseTokens > 0 {
		args = append(args, "--cache-reuse", strconv.Itoa(m.CacheReuseTokens))
	}

	if m.SpeculativeMode == "ngram" {
		args = append(args, "--spec-type", "ngram-simple")
		if m.SpeculativeTokens > 0 {
			args = append(args, "--spec-ngram-simple-size-m", strconv.Itoa(m.SpeculativeTokens))
		}
	} else if m.SpeculativeMode == "draft" && m.DraftModelPath != "" {
		// Draft stays on Host CPU to avoid allocating another model on the
		// active RPC Guest backends. GGML validates vocabulary compatibility.
		args = append(args, "--spec-type", "draft-simple", "--spec-draft-model", m.DraftModelPath, "--spec-draft-ngl", "0")
		if m.SpeculativeTokens > 0 {
			args = append(args, "--spec-draft-n-max", strconv.Itoa(m.SpeculativeTokens))
		}
	}
	switch {
	case m.MoECPU:
		args = append(args, "--cpu-moe")
	case m.MoECPULayers > 0:
		args = append(args, "--n-cpu-moe", strconv.Itoa(m.MoECPULayers))
	}
	if m.MoECacheMiB > 0 {
		args = append(args, "--moe-cache-mib", strconv.Itoa(m.MoECacheMiB))
		if m.MoECacheCPUShare > 0 {
			args = append(args, "--moe-cache-cpu-share", strconv.FormatFloat(m.MoECacheCPUShare, 'f', -1, 64))
		}
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
		if len(m.RPCDeviceNames) > 0 {
			args = append(args, "--device", strings.Join(m.RPCDeviceNames, ","))
			if m.RPCTensorSplit != "" {
				args = append(args, "--tensor-split", m.RPCTensorSplit)
			}
		}
	}

	return args
}

func (m *Managed) parallelRPCUploadEligible() bool {
	if len(m.RPCServers) > 1 {
		return true
	}
	if len(m.RPCServers) != 1 {
		return false
	}
	// The pinned loader keys workers by ggml_backend_dev_t, not endpoint.
	// Count only explicitly selected RPC devices, not a local GPU.
	count := 0
	for _, name := range m.RPCDeviceNames {
		if strings.HasPrefix(name, "RPC") {
			count++
		}
	}
	return count > 1
}

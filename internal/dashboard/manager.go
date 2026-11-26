// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	urlpath "path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/catalog"
	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

// Settings are persisted independently of the UI. The model weights live only
// in the llama-server child process and can be unloaded without stopping NodexLM.
type Settings struct {
	Backend           string  `json:"backend,omitempty"`
	Model             string  `json:"model"`
	Threads           int     `json:"threads"`
	ThreadsBatch      int     `json:"threads_batch,omitempty"`
	ParallelSlots     int     `json:"parallel_slots,omitempty"`
	PromptCacheMiB    int     `json:"prompt_cache_mib,omitempty"`
	LoadMode          string  `json:"load_mode,omitempty"`
	LazyMode          string  `json:"lazy_mode,omitempty"`
	NUMA              string  `json:"numa,omitempty"`
	ContextTokens     int     `json:"context_tokens"`
	GPULayers         string  `json:"gpu_layers"`
	HostGPU           string  `json:"host_gpu,omitempty"`
	MoECPU            bool    `json:"moe_cpu"`
	MoECPULayers      int     `json:"moe_cpu_layers"`
	MoECacheMiB       int     `json:"moe_cache_mib"`
	MoECacheCPUShare  float64 `json:"moe_cache_cpu_share,omitempty"`
	Distributed       bool    `json:"distributed"`
	PlacementStrategy string  `json:"placement_strategy,omitempty"`
	PrimaryNode       string  `json:"primary_node,omitempty"`
	FlashAttention    string  `json:"flash_attention,omitempty"`
	KVCacheType       string  `json:"kv_cache_type,omitempty"`
	BatchSize         int     `json:"batch_size,omitempty"`
	UBatchSize        int     `json:"ubatch_size,omitempty"`
	CacheReuseTokens  int     `json:"cache_reuse_tokens,omitempty"`
	SpeculativeTokens int     `json:"speculative_tokens,omitempty"`
	SpeculativeMode   string  `json:"speculative_mode,omitempty"`
	DraftModel        string  `json:"draft_model,omitempty"`
	VisionProjector   string  `json:"vision_projector,omitempty"`
}

// managedFromSettings keeps native options identical for loads and benchmarks.
// RPC placement remains a caller-specific override.
func managedFromSettings(s Settings, binary, modelPath, alias, draftPath string) *local.Managed {
	var projector string
	if s.VisionProjector != "" {
		projector = filepath.Join(filepath.Dir(modelPath), s.VisionProjector)
	}
	return &local.Managed{
		Backend: s.Backend, Binary: binary, ModelPath: modelPath, Alias: alias,
		Threads: s.Threads, ThreadsBatch: s.ThreadsBatch, ParallelSlots: s.ParallelSlots, ContextTokens: s.ContextTokens, GPULayers: s.GPULayers,
		PromptCacheMiB: s.PromptCacheMiB, LoadMode: s.LoadMode, LazyMode: s.LazyMode, NUMA: s.NUMA,
		MoECPU: s.MoECPU, MoECPULayers: s.MoECPULayers, MoECacheMiB: s.MoECacheMiB,
		MoECacheCPUShare: s.MoECacheCPUShare,
		FlashAttention:   s.FlashAttention, KVCacheType: s.KVCacheType,
		BatchSize: s.BatchSize, UBatchSize: s.UBatchSize, CacheReuseTokens: s.CacheReuseTokens,
		SpeculativeTokens: s.SpeculativeTokens,
		SpeculativeMode:   s.SpeculativeMode, DraftModelPath: draftPath, KeepLoaded: true,
		VisionProjectorPath: projector,
	}
}

type Download struct {
	ShardCount  int                               `json:"shard_count,omitempty"`
	Name        string                            `json:"name"`
	Received    int64                             `json:"received"`
	Total       int64                             `json:"total"`
	Status      string                            `json:"status"`
	Error       string                            `json:"error,omitempty"`
	Connections []safedownload.ConnectionProgress `json:"connections,omitempty"`
	SpeedBPS    int64                             `json:"speed_bps,omitempty"`
}

type Manager struct {
	poolCancel            context.CancelFunc
	mu                    sync.Mutex
	poolMu                sync.Mutex
	residents             map[string]*Manager
	closed                bool
	samples               map[string]InferenceSample
	microbenchAttempts    map[string]time.Time
	microbenchDrift       int
	loadFingerprint       string
	loadedTopology        *inferenceTopology
	activityWindow        []activityObservation
	generationPhases      map[uint64]string
	microbenchNode        string
	generationTokens      uint64
	generationBuckets     [8]uint64
	generationCount       uint64
	generationErrors      uint64
	generationSeconds     float64
	chatMu                sync.RWMutex
	admission             chan struct{}
	dir, modelDir, binary string
	settings              Settings
	engine                *local.Managed
	current               string
	nativeSystemInfo      string
	loading               bool
	unloading             bool
	loadPhase             string
	loadError             string
	loadStartedAt         time.Time
	phaseStartedAt        time.Time
	loadRPCNodes          []string
	loadUploadPlan        *inferenceTopology
	loadRPCBaseline       map[string]rpcmesh.TunnelStats
	rpcStatsProvider      func() map[string]rpcmesh.TunnelStats
	loadCancel            context.CancelFunc
	loadCancelRequested   bool
	loadDone              chan struct{}
	generations           map[uint64]context.CancelFunc
	generationSequence    uint64
	download              *Download
	downloadMeter         *safedownload.TransferMeter
	cancel                context.CancelFunc
	downloadDone          chan struct{}
	httpClient            *http.Client
	rpcEndpoints          func() []RPCGuest
}

func NewManager(dir, modelDir, binary string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(modelDir, 0700); err != nil {
		return nil, err
	}
	m := &Manager{residents: map[string]*Manager{}, samples: map[string]InferenceSample{}, admission: make(chan struct{}, 1), dir: dir, modelDir: modelDir, binary: binary, settings: Settings{Threads: min(256, max(1, hardware.PhysicalCPUCount())), ContextTokens: 2048, GPULayers: "auto", PlacementStrategy: "speed"}}
	b, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if err == nil {
		if err = json.Unmarshal(b, &m.settings); err != nil {
			return nil, fmt.Errorf("runtime settings: %w", err)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err = m.validate(m.settings); err != nil {
		return nil, fmt.Errorf("runtime settings: %w", err)
	}
	m.admission = make(chan struct{}, max(1, m.settings.ParallelSlots))
	m.loadBenchmarks()
	m.httpClient = safedownload.Client()
	return m, nil
}

// RPCGuest is one authenticated, currently connected device backed by a
// loopback-only relay. CapacityBytes is a conservative available weight budget.
// No model bytes are copied to the Guest filesystem.
type RPCGuest struct {
	ID            string
	Name          string
	Endpoint      string
	CapacityBytes uint64
	// RPCDeviceKinds mirrors the worker's native ggml-rpc-server order.
	// nil means unknown/legacy: never assume whether that Guest exposes CPU.
	RPCDeviceKinds []string
	RAMBudgetBytes uint64
	GPUBudgetBytes uint64
}

func (m *Manager) SetRPCProvider(provider func() []RPCGuest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rpcEndpoints = provider
}

// SetRPCStatsProvider exposes only local relay counters, never RPC payloads.
func (m *Manager) SetRPCStatsProvider(provider func() map[string]rpcmesh.TunnelStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rpcStatsProvider = provider
}
func (m *Manager) Models() ([]protocol.ModelRecord, error) { return local.Models(m.modelDir) }
func (m *Manager) Settings() Settings                      { m.mu.Lock(); defer m.mu.Unlock(); return m.settings }
func (m *Manager) State() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	phase := m.loadPhase
	if phase == "" {
		phase = "idle"
	}
	state := map[string]any{"backend": local.EffectiveBackend(m.settings.Backend), "model": m.current, "loaded": m.engine != nil, "loading": m.loading, "phase": phase, "error": m.loadError, "in_flight": len(m.generations)}
	if m.nativeSystemInfo != "" {
		state["native_system_info"] = m.nativeSystemInfo
	}
	if m.loading {
		state["started_at"] = m.loadStartedAt.UTC().Format(time.RFC3339)
		state["elapsed_seconds"] = int(time.Since(m.loadStartedAt).Seconds())
		state["progress_known"] = false // GGML does not report per-Guest tensor transfer progress.
		if m.loadUploadPlan != nil {
			state["upload_plan"] = m.loadUploadPlan
		}
		state["phase_elapsed_seconds"] = int(time.Since(m.phaseStartedAt).Seconds())
		if m.rpcStatsProvider != nil && len(m.loadRPCNodes) > 0 {
			current := m.rpcStatsProvider()
			nodes := make(map[string]map[string]any, len(m.loadRPCNodes))
			for _, id := range m.loadRPCNodes {
				now, baseline := current[id], m.loadRPCBaseline[id]
				sent, received := uint64(0), uint64(0)
				if now.BytesToGuest >= baseline.BytesToGuest {
					sent = now.BytesToGuest - baseline.BytesToGuest
				}
				if now.BytesFromGuest >= baseline.BytesFromGuest {
					received = now.BytesFromGuest - baseline.BytesFromGuest
				}
				last := now.LastActivityMS
				if last < m.loadStartedAt.UnixMilli() {
					last = 0
				}
				nodes[id] = map[string]any{"to_guest_bytes": sent, "from_guest_bytes": received,
					"connected": now.Connected, "idle": now.Idle, "last_activity_ms": last,
					"wait_timeouts": now.WaitTimeouts - min(now.WaitTimeouts, baseline.WaitTimeouts)}
			}
			state["rpc_transfer"] = nodes
		}
	}
	return state
}
func (m *Manager) DownloadState() *Download {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.download == nil {
		return nil
	}
	s := *m.download
	if s.Status == "downloading" {
		s.Connections, s.SpeedBPS = m.downloadMeter.Snapshot()
	}
	return &s
}
func (m *Manager) validate(s Settings) error {
	if s.VisionProjector != "" {
		if local.EffectiveBackend(s.Backend) != local.BackendLlama ||
			s.SpeculativeMode == "draft" || s.SpeculativeMode == "ngram" || s.ParallelSlots > 1 {
			return errors.New("vision requires llama.cpp, one slot and no speculative decoding")
		}
		if s.Model == "" || s.VisionProjector == s.Model {
			return errors.New("select a language model and a separate vision projector")
		}
		model, err := modelPath(m.modelDir, s.Model)
		if err != nil {
			return err
		}
		projector, err := modelPath(m.modelDir, s.VisionProjector)
		if err != nil {
			return err
		}
		if err := local.ValidateVisionProjector(projector, model); err != nil {
			return err
		}
	}
	if explicitPrimary(s.PrimaryNode) && s.PrimaryNode != "host" && !s.Distributed {
		return errors.New("Guest principal exige distribuição RPC habilitada")
	}
	backend := local.EffectiveBackend(s.Backend)
	if backend != local.BackendLlama && backend != local.BackendMistralRS {
		return errors.New("unsupported backend; select llama.cpp or mistralrs")
	}
	if backend == local.BackendMistralRS && s.Distributed {
		return errors.New("mistral.rs cannot distribute GGUF over NodexLM RPC Guests; select llama.cpp for distributed inference")
	}
	if s.Threads < 1 || s.Threads > 256 || s.ContextTokens < 256 || s.ContextTokens > 262144 || s.MoECPULayers < 0 || s.MoECPULayers > 1024 || s.MoECacheMiB < 0 || s.MoECacheMiB > 65536 || s.MoECPU && s.MoECPULayers > 0 || (s.PlacementStrategy != "" && s.PlacementStrategy != "capacity" && s.PlacementStrategy != "speed") {
		return errors.New("invalid runtime limits or conflicting MoE settings")
	}
	if !(s.MoECacheCPUShare >= 0 && s.MoECacheCPUShare <= 1) ||
		(s.MoECacheCPUShare > 0 && (s.MoECacheMiB == 0 || !s.MoECPU && s.MoECPULayers == 0)) {
		return errors.New("moe_cache_cpu_share must be 0..1 and requires an enabled MoE cache")
	}
	if err := validateInferenceResources(s); err != nil {
		return err
	}
	if s.CacheReuseTokens < 0 || s.CacheReuseTokens > 4096 {
		return errors.New("cache_reuse_tokens must be 0..4096")
	}
	if s.SpeculativeTokens < 0 || s.SpeculativeTokens > 32 || (s.SpeculativeTokens > 0 && s.SpeculativeMode != "draft" && s.SpeculativeMode != "ngram") {
		return errors.New("speculative_tokens must be 0..32 and requires draft or ngram mode")
	}
	if s.FlashAttention != "" && s.FlashAttention != "auto" && s.FlashAttention != "on" && s.FlashAttention != "off" {
		return errors.New("flash_attention must be auto, on or off")
	}
	if s.KVCacheType != "" && s.KVCacheType != "f16" && s.KVCacheType != "q8_0" && s.KVCacheType != "q4_0" {
		return errors.New("kv_cache_type must be f16, q8_0 or q4_0")
	}
	if s.BatchSize < 0 || s.BatchSize > 8192 || s.UBatchSize < 0 || s.UBatchSize > 4096 ||
		(s.BatchSize > 0 && s.UBatchSize > s.BatchSize) {
		return errors.New("invalid batch_size or ubatch_size")
	}
	if s.HostGPU != "" && s.HostGPU != "auto" && s.HostGPU != "cpu" && s.HostGPU != "vulkan" && s.HostGPU != "metal" && s.HostGPU != "cuda" {
		return errors.New("host_gpu must be auto, cpu, vulkan, metal or cuda")
	}
	if s.SpeculativeMode != "" && s.SpeculativeMode != "off" && s.SpeculativeMode != "ngram" && s.SpeculativeMode != "draft" {
		return errors.New("speculative_mode must be off, ngram or draft")
	}
	if s.SpeculativeMode == "draft" {
		if s.DraftModel == "" || s.DraftModel == s.Model {
			return errors.New("draft mode needs a separate GGUF model")
		}
		if _, err := modelPath(m.modelDir, s.DraftModel); err != nil {
			return err
		}
	} else if s.DraftModel != "" {
		if _, err := modelPath(m.modelDir, s.DraftModel); err != nil {
			return err
		}
	}
	if s.GPULayers != "auto" && s.GPULayers != "all" {
		if parsed, err := strconv.Atoi(s.GPULayers); err != nil || parsed < 0 || parsed > 1024 || fmt.Sprint(parsed) != s.GPULayers {
			return errors.New("invalid gpu_layers")
		}
	}
	if s.Model != "" {
		if _, err := modelPath(m.modelDir, s.Model); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) Update(s Settings) error {
	if err := m.validate(s); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil || m.loading || m.unloading {
		return errors.New("unload model before changing runtime settings")
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(m.dir, "runtime.json"), raw); err != nil {
		return err
	}
	// Benchmarks are only comparable when the native inference configuration
	// is unchanged. Do not allow stale tokens/s to drive placement decisions.
	old := m.settings
	if old.PrimaryNode != s.PrimaryNode || old.Backend != s.Backend || old.Threads != s.Threads || old.ContextTokens != s.ContextTokens ||
		old.ThreadsBatch != s.ThreadsBatch || old.ParallelSlots != s.ParallelSlots || old.PromptCacheMiB != s.PromptCacheMiB || old.LoadMode != s.LoadMode || old.LazyMode != s.LazyMode || old.NUMA != s.NUMA ||
		old.GPULayers != s.GPULayers || old.MoECPU != s.MoECPU ||
		old.MoECPULayers != s.MoECPULayers || old.MoECacheMiB != s.MoECacheMiB || old.MoECacheCPUShare != s.MoECacheCPUShare ||
		old.FlashAttention != s.FlashAttention || old.KVCacheType != s.KVCacheType ||
		old.BatchSize != s.BatchSize || old.UBatchSize != s.UBatchSize ||
		old.CacheReuseTokens != s.CacheReuseTokens || old.SpeculativeTokens != s.SpeculativeTokens ||
		old.SpeculativeMode != s.SpeculativeMode || old.DraftModel != s.DraftModel || old.VisionProjector != s.VisionProjector {
		m.samples = make(map[string]InferenceSample)
	}
	m.settings = s
	m.admission = make(chan struct{}, max(1, s.ParallelSlots))
	return nil
}
func writePrivate(path string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".runtime-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func modelPath(dir, name string) (string, error) {
	if name == "" || filepath.Base(name) != name || !strings.EqualFold(filepath.Ext(name), ".gguf") || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
		return "", errors.New("model name must be a GGUF file in the models directory")
	}
	path := filepath.Join(dir, name)
	// Do not follow symlinks out of the managed model directory.
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return "", errors.New("model must be a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

// StartLoad starts an expensive model preparation outside the HTTP request.
// The dashboard polls /api/status for its phase and any failure.
func (m *Manager) StartLoad() error {
	m.mu.Lock()
	distributed := m.settings.Distributed
	m.mu.Unlock()
	limit := 30 * time.Minute
	if distributed {
		limit = 90 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	if err := m.beginLoad(cancel); err != nil {
		cancel()
		return err
	}
	slog.Info("Model load accepted", "distributed", distributed, "timeout_minutes", int(limit.Minutes()))
	go func() {
		defer cancel()
		m.runLoad(ctx)
	}()
	return nil
}

func (m *Manager) beginLoad(cancel context.CancelFunc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.residents) >= 4 {
		return errors.New("at most four models may be resident")
	}
	if m.closed || m.residents[m.settings.Model] != nil || m.loading || m.unloading || m.engine != nil {
		return errors.New("model already loaded or loading")
	}
	if m.settings.Model == "" {
		return errors.New("select a model first")
	}
	if path, err := modelPath(m.modelDir, m.settings.Model); err != nil {
		return err
	} else if _, err = os.Stat(path); err != nil {
		return err
	}
	m.loading = true
	m.loadStartedAt = time.Now()
	m.phaseStartedAt = m.loadStartedAt
	m.loadRPCNodes = nil
	m.loadUploadPlan = nil
	m.loadRPCBaseline = nil
	m.loadFingerprint = ""
	m.microbenchDrift = 0
	if m.rpcStatsProvider != nil {
		m.loadRPCBaseline = m.rpcStatsProvider()
	}
	m.loadPhase = "preparing_runtime"
	m.loadError = ""
	m.loadCancel = cancel
	m.loadCancelRequested = false
	m.loadDone = make(chan struct{})
	return nil
}

// Load preserves the synchronous API for callers outside the dashboard.
func (m *Manager) Load(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := m.beginLoad(cancel); err != nil {
		return err
	}
	return m.runLoad(ctx)
}

// CancelLoad affects only the primary load, never the pool's benchmark,
// autotune, microbenchmark or additional resident operation. Publishing the
// engine and cancelling are serialized by mu; a completed load is untouched.
func (m *Manager) CancelLoad() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.loading || m.engine != nil || m.loadCancel == nil {
		return false
	}
	m.loadCancelRequested = true
	m.loadPhase = "cancelling"
	m.phaseStartedAt = time.Now()
	m.loadCancel()
	return true
}

func (m *Manager) runLoad(ctx context.Context) error {
	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	err := ctx.Err()
	if err == nil {
		err = m.load(ctx)
	}
	m.mu.Lock()
	duration := time.Since(m.loadStartedAt)
	m.loading = false
	m.loadCancel = nil
	cancelled := err != nil && (m.loadCancelRequested || errors.Is(ctx.Err(), context.Canceled))
	m.loadCancelRequested = false
	m.loadUploadPlan = nil
	if cancelled {
		m.loadPhase = "cancelled"
		m.loadError = ""
		m.loadRPCNodes = nil
		m.loadRPCBaseline = nil
		m.loadFingerprint = ""
	} else if err != nil {
		m.loadPhase = "error"
		m.loadError = err.Error()
	} else {
		m.loadPhase = "ready"
		m.loadError = ""
	}
	close(m.loadDone)
	m.loadDone = nil
	m.mu.Unlock()
	if cancelled {
		slog.Info("Model load cancelled", "elapsed_seconds", int(duration.Seconds()))
	} else if err != nil {
		slog.Error("Model load failed", "elapsed_seconds", int(duration.Seconds()), "error", err)
	} else {
		slog.Info("Model load completed", "elapsed_seconds", int(duration.Seconds()))
	}
	return err
}

func (m *Manager) setPhase(phase string) {
	m.mu.Lock()
	if !m.loadCancelRequested {
		m.loadPhase = phase
		m.phaseStartedAt = time.Now()
	}
	m.mu.Unlock()
	slog.Info("Model load phase", "phase", phase)
}

func (m *Manager) load(ctx context.Context) error {
	m.mu.Lock()
	// Recheck after admission: an additional load may have completed while
	// this primary load was waiting for poolMu.
	if len(m.residents) >= 4 {
		m.mu.Unlock()
		return errors.New("at most four models may be resident")
	}
	s := m.settings
	provider := m.rpcEndpoints
	m.mu.Unlock()
	path, err := modelPath(m.modelDir, s.Model)
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); err != nil {
		return err
	}
	rpcEndpoints := []string(nil)
	localLayers := 0
	var devicePlacement RPCDevicePlacement
	var modelBlocks uint32
	var loadedMeta local.GGUFInfo
	var loadedSize, loadedBudget uint64
	var loadedSelection clusterplan.Selection
	var loadedGuests []RPCGuest
	if s.Distributed {
		m.setPhase("selecting_guests")
		// The Host can still load locally if there are no RPC Guests,
		// independently of a previously saved capacity strategy.
		var guests []RPCGuest
		if provider != nil {
			guests = provider()
		}
		model, err := local.InspectGGUF(path)
		if err != nil {
			return err
		}
		if model.BlockCount == 0 {
			return errors.New("model lacks GGUF block_count needed for distributed placement")
		}
		size, err := local.ModelSize(path)
		if err != nil {
			return err
		}
		name, _ := os.Hostname()
		hostCapacity, budgetErr := m.hostBudget(s, hardware.Collect(name, nil))
		if budgetErr != nil {
			return budgetErr
		}
		selection, selectErr := m.chooseRPC(s, model, uint64(size), hostCapacity, guests)
		if selectErr != nil {
			return selectErr
		}
		localLayers = selection.LocalLayers
		devicePlacement = planRPCDevices(selection, guests, model, s, uint64(size))
		modelBlocks = model.BlockCount
		loadedMeta, loadedSize, loadedBudget = model, uint64(size), hostCapacity
		loadedSelection, loadedGuests = selection, guests
		selectedIDs := make([]string, 0, len(selection.Guests))
		for _, candidate := range selection.Guests {
			selectedIDs = append(selectedIDs, candidate.ID)
			for _, guest := range guests {
				if guest.ID == candidate.ID {
					rpcEndpoints = append(rpcEndpoints, guest.Endpoint)
					break
				}
			}
		}
		if len(rpcEndpoints) != len(selection.Guests) {
			return errors.New("RPC relay became unavailable while planning layers; reconnect the selected Guests")
		}
		slog.Info("Model placement planned", "selected_guests", len(selection.Guests),
			"available_guests", len(guests), "host_layers", localLayers, "total_layers", model.BlockCount,
			"strategy", effectivePlacementStrategy(s.PlacementStrategy), "using_rpc", len(selection.Guests) > 0,
			"rpc_devices", devicePlacement.DeviceArg(), "device_modes", devicePlacement.Modes,
			"parallel_rpc_upload", len(selection.Guests) > 1 && llamaruntime.BundledParallelRPCUploadsSupported())
		m.mu.Lock()
		m.loadRPCNodes = selectedIDs
		m.mu.Unlock()
		if len(rpcEndpoints) > 0 {
			m.setPhase("verifying_rpc_guests")
			if err := preflightRPCGuests(ctx, selection.Guests, rpcEndpoints, guests...); err != nil {
				return err
			}
			// HELLO and DEVICE_COUNT are not model tensor uploads. Reset the
			// transfer baseline so the dashboard only shows bytes from actual
			// model loading, not from the preflight handshake.
			m.mu.Lock()
			if m.rpcStatsProvider != nil {
				m.loadRPCBaseline = m.rpcStatsProvider()
			}
			m.mu.Unlock()
		}
	} else {
		// The preview and the actual local load must use the same memory
		// guard. Do not silently ignore high-context KV on the Host.
		meta, inspectErr := local.InspectGGUF(path)
		if inspectErr != nil {
			return inspectErr
		}
		size, sizeErr := local.ModelSize(path)
		if sizeErr != nil {
			return sizeErr
		}
		name, _ := os.Hostname()
		hostCapacity, budgetErr := m.hostBudget(s, hardware.Collect(name, nil))
		if budgetErr != nil {
			return budgetErr
		}
		loadedMeta, loadedSize, loadedBudget = meta, uint64(size), hostCapacity
		if inspectErr = validatePlacementMemory(meta, s, uint64(size), hostCapacity, nil); inspectErr != nil {
			return inspectErr
		}
	}
	m.setPhase("resolving_runtime")
	var resolved string
	// Speed preference may legitimately choose Host-only while the user
	// keeps distributed capability enabled in settings.
	usingRPC := s.Distributed && len(rpcEndpoints) > 0
	if local.EffectiveBackend(s.Backend) == local.BackendMistralRS {
		// This opt-in backend is separately resolved by local.Managed.
		resolved = ""
	} else if usingRPC && (m.binary == "" || m.binary == "llama-server") {
		resolved, _, err = llamaruntime.EnsureDistributed(ctx, m.dir, effectiveHostRuntimeBackend(s.HostGPU))
	} else if (m.binary == "" || m.binary == "llama-server") && s.HostGPU == "cuda" {
		resolved, _, err = llamaruntime.EnsureDistributed(ctx, m.dir, "cuda")
	} else if (m.binary == "" || m.binary == "llama-server") && (s.HostGPU == "vulkan" || s.HostGPU == "metal") {
		resolved, _, err = llamaruntime.Ensure(ctx, m.dir, s.HostGPU)
	} else {
		resolved, err = llamaruntime.AutoExecutable(ctx, m.dir, m.binary)
	}
	if err != nil {
		return err
	}
	if s.HostGPU == "cuda" && s.GPULayers != "0" && discoverHostGPU(ctx, resolved, "cuda") == "" {
		return errors.New("explicit CUDA requires exactly one initialized native CUDA device; use CUDA_VISIBLE_DEVICES to select it")
	}
	if usingRPC && !llamaruntime.SupportsRPC(ctx, resolved) {
		return errors.New("llama-server was built without GGML_RPC; use the bundled RPC runtime")
	}
	if usingRPC && len(devicePlacement.Devices) > 0 && localLayers > 0 && s.GPULayers != "0" {
		device := discoverHostGPU(ctx, resolved, s.HostGPU)
		devicePlacement, localLayers = withHostGPU(devicePlacement, localLayers, modelBlocks, s.HostGPU, device, s.GPULayers)
	}
	draftPath, err := m.resolveDraftModel(s)
	if err != nil {
		return err
	}
	m.setPhase("loading_model")
	engine := managedFromSettings(s, resolved, path, s.Model, draftPath)
	if usingRPC {
		engine.RPCServers = rpcEndpoints
		engine.RPCLocalLayers = localLayers
		engine.RPCDeviceNames = devicePlacement.Devices
		engine.RPCTensorSplit = devicePlacement.TensorSplitArg()
		if s.GPULayers == "auto" {
			engine.GPULayers = "all"
		}
	}
	m.mu.Lock()
	m.loadUploadPlan = buildInferenceTopology(s, loadedMeta, loadedSize, loadedBudget, loadedSelection, loadedGuests, devicePlacement, engine)
	m.mu.Unlock()
	loadFingerprint := m.benchmarkSignature(s, s.Model)
	if err = engine.Load(ctx); err != nil {
		engine.Close()
		return err
	}
	systemInfo := engine.SystemInfo()
	m.mu.Lock()
	if ctx.Err() != nil || m.unloading {
		m.mu.Unlock()
		engine.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("model load cancelled")
	}
	m.engine = engine
	m.loadedTopology = m.loadUploadPlan
	m.loadUploadPlan = nil
	m.activityWindow = nil
	m.loadFingerprint = loadFingerprint
	m.nativeSystemInfo = systemInfo
	m.current = s.Model
	m.mu.Unlock()
	return nil
}
func (m *Manager) Unload() {
	m.mu.Lock()
	m.unloading = true
	cancelLoad := m.loadCancel
	loadDone := m.loadDone
	for _, cancel := range m.generations {
		cancel()
	}
	m.mu.Unlock()
	if cancelLoad != nil {
		cancelLoad()
	}
	if loadDone != nil {
		<-loadDone
	}
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	m.mu.Lock()
	engine := m.engine
	m.engine = nil
	m.loadedTopology = nil
	m.activityWindow = nil
	m.nativeSystemInfo = ""
	m.current = ""
	m.loadPhase = "idle"
	m.loadError = ""
	m.unloading = false
	m.mu.Unlock()
	if engine != nil {
		engine.Close()
	}
}
func (m *Manager) Generate(ctx context.Context, req protocol.ChatRequest) (string, error) {

	var answer string
	err := m.withGeneration(ctx, req, func(ctx context.Context, engine *local.Managed, req protocol.ChatRequest) error {
		var err error
		answer, err = engine.Generate(ctx, req)
		return err
	})
	return answer, err
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	poolCancel := m.poolCancel
	children := m.residents
	m.residents = map[string]*Manager{}
	done := m.downloadDone
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	if poolCancel != nil {
		poolCancel()
	}
	for _, child := range children {
		child.Close()
	}
	m.Unload()
	m.poolMu.Lock()
	m.poolMu.Unlock()
}
func (m *Manager) StartDownload(urlText, name string) error {
	if name == "" {
		return errors.New("filename required")
	}
	path, err := modelPath(m.modelDir, name)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(path); err == nil {
		return errors.New("model file already exists")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	u, err := parseModelURL(urlText)
	if err != nil {
		return err
	}
	parts, err := catalog.ShardNames(urlpath.Base(u.Path))
	if err != nil {
		return err
	}
	if len(parts) > 1 && parts[0] != name {
		return errors.New("sharded GGUF destination must use the original first-shard filename")
	}
	for _, part := range parts {
		if _, err := os.Lstat(filepath.Join(m.modelDir, part)); err == nil {
			return fmt.Errorf("GGUF shard %s already exists", part)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.download != nil && m.download.Status == "downloading" {
		return errors.New("another download is in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = safedownload.WithBandwidthPolicy(ctx, func() int64 {
		m.mu.Lock()
		defer m.mu.Unlock()
		// Keep WAN downloads from competing with loading and active decoding.
		// Do not throttle merely because a resident model is idle.
		if m.loading || len(m.generations) > 0 {
			return 2 << 20
		}
		return 0
	})
	m.cancel = cancel
	m.downloadDone = make(chan struct{})
	m.download = &Download{Name: name, Status: "downloading", ShardCount: len(parts)}
	m.downloadMeter = safedownload.NewTransferMeter()
	ctx = safedownload.WithTransferMeter(ctx, m.downloadMeter)
	go m.downloadModel(ctx, u.String(), path, name)
	return nil
}

// downloadModel stages every shard before publishing anything to the models
// directory. If a part fails, no incomplete model can become selectable.
func (m *Manager) downloadModel(ctx context.Context, source, destination, name string) {
	status := "failed"
	errorText := ""
	defer func() {
		m.mu.Lock()
		if ctx.Err() != nil {
			status = "cancelled"
		}
		m.download.Status = status
		m.download.Error = errorText
		m.downloadMeter = nil
		m.cancel = nil
		close(m.downloadDone)
		m.downloadDone = nil
		m.mu.Unlock()
	}()
	release, err := safedownload.LockTransfer(safedownload.StagePath(m.modelDir, source) + ".lock")
	if err != nil {
		errorText = err.Error()
		return
	}
	defer release()
	sourceURL, err := url.Parse(source)
	if err != nil {
		errorText = err.Error()
		return
	}
	shards, err := catalog.ShardNames(urlpath.Base(sourceURL.Path))
	if err != nil {
		errorText = err.Error()
		return
	}
	stage := safedownload.StagePath(m.modelDir, source) + ".d"
	err = os.MkdirAll(stage, 0700)
	if err != nil {
		errorText = err.Error()
		return
	}
	const limit int64 = 150 << 30
	var received int64
	files := make([]safedownload.BatchFile, 0, len(shards))
	for _, shard := range shards {
		partURL := *sourceURL
		if len(shards) > 1 {
			partURL.Path = urlpath.Join(urlpath.Dir(sourceURL.Path), shard)
			partURL.RawPath = ""
		}
		files = append(files, safedownload.BatchFile{Source: partURL.String(), Target: filepath.Join(stage, shard)})
	}
	batch, handled, err := safedownload.PlanHuggingFaceBatch(ctx, m.httpClient, files)
	if err != nil {
		errorText = err.Error()
		return
	}
	if handled {
		err = safedownload.FetchBatch(ctx, m.httpClient, batch, limit, func(n, total int64) {
			m.mu.Lock()
			m.download.Received = n
			m.download.Total = total
			m.mu.Unlock()
		})
		if err != nil {
			errorText = err.Error()
			return
		}
		for _, part := range batch {
			received += part.Size
		}
	} else {
		for _, part := range files {
			n, err := m.fetchModelPart(ctx, part.Source, part.Target, received, limit)
			if err != nil {
				errorText = fmt.Sprintf("GGUF shard %s: %v", filepath.Base(part.Target), err)
				return
			}
			received += n
		}
	}
	// The first shard holds the architecture metadata; the other parts must
	// be valid GGUF containers but may not repeat all of that metadata.
	if _, err = local.InspectGGUF(filepath.Join(stage, shards[0])); err != nil {
		errorText = "invalid GGUF: " + err.Error()
		return
	}
	created := make([]string, 0, len(shards))
	defer func() {
		if status != "completed" {
			for _, item := range created {
				_ = os.Remove(item)
			}
		}
	}()
	for i := len(shards) - 1; i >= 0; i-- {
		shard := shards[i]
		target := filepath.Join(m.modelDir, shard)
		if len(shards) == 1 {
			target = destination
		}
		if err = publishModelPart(filepath.Join(stage, shard), target); err != nil {
			errorText = fmt.Sprintf("publish GGUF shard %s: %v", shard, err)
			return
		}
		created = append(created, target)
	}
	m.mu.Lock()
	m.download.Received = received
	m.download.Total = received
	m.mu.Unlock()
	status = "completed"
	_ = os.RemoveAll(stage)
}

// fetchModelPart streams one bounded shard to a private staging file. A
// Hugging Face LFS SHA-256 header is verified whenever it is provided.
func (m *Manager) fetchModelPart(ctx context.Context, source, target string, offset, limit int64) (int64, error) {
	return safedownload.Fetch(ctx, m.httpClient, source, target, limit-offset, func(n, total int64) {
		m.mu.Lock()
		m.download.Received = offset + n
		if total > 0 && m.download.ShardCount <= 1 {
			m.download.Total = offset + total
		} else {
			m.download.Total = 0
		}
		m.mu.Unlock()
	})
}

// publishModelPart is exclusive even on filesystems that do not implement
// hard links. O_EXCL prevents overwriting a concurrently created model.
func publishModelPart(stage, target string) error { return catalog.PublishFile(stage, target) }

func (m *Manager) CancelDownload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

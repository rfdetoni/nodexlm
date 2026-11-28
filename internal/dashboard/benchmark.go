package dashboard

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

// medianInferenceSample chooses a real measured run, not an average that
// could mask a first-token or scheduling outlier.
func medianInferenceSample(samples []InferenceSample) InferenceSample {
	sort.SliceStable(samples, func(i, j int) bool {
		return samples[i].TokensPerSecond < samples[j].TokensPerSecond
	})
	return samples[len(samples)/2]
}

// BenchmarkInference runs the same bounded text workload locally or on one
// authenticated RPC Guest with the configured context and runtime options.
// Report decode measurements separately from live end-to-end timings.
// It must run with resident models unloaded, avoiding an undisclosed eviction.
func (m *Manager) BenchmarkInference(ctx context.Context, model, nodeID string) (InferenceSample, error) {
	return m.benchmarkInference(ctx, model, nodeID, false)
}

func (m *Manager) benchmarkInference(ctx context.Context, model, nodeID string, extended bool) (InferenceSample, error) {
	rows, err := m.benchmarkSeries(ctx, model, nodeID, extended, 1)
	if err != nil {
		return InferenceSample{}, err
	}
	return rows[0].Inference, nil
}

type benchmarkRound struct {
	Inference      InferenceSample                `json:"inference"`
	RPCStatsBefore map[string]rpcmesh.TunnelStats `json:"rpc_stats_before"`
	RPCStatsAfter  map[string]rpcmesh.TunnelStats `json:"rpc_stats_after"`
}

// Keep the same admitted native placement across unloaded rounds. Do not
// persist a partial series or reuse its topology after this request returns.
func (m *Manager) benchmarkSeries(ctx context.Context, model, nodeID string, extended bool, rounds int) ([]benchmarkRound, error) {
	return m.benchmarkSeriesDuration(ctx, model, nodeID, extended, rounds, 0)
}

func (m *Manager) benchmarkSeriesDuration(ctx context.Context, model, nodeID string, extended bool, rounds int, duration time.Duration) ([]benchmarkRound, error) {
	if rounds < 1 || rounds > 10 || rounds > 1 && (nodeID != "cluster" || extended) {
		return nil, errors.New("benchmark series requires 1–10 rounds; multiple rounds require cluster without extended workloads")
	}
	benchmarkPhase(ctx, "queue", "")
	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.mu.Lock()
	m.poolCancel = cancel
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.poolCancel = nil; m.mu.Unlock() }()
	if len(m.LoadedModels()) != 0 {
		return nil, errors.New("unload resident models before inference benchmark")
	}
	m.mu.Lock()
	settings := m.settings
	provider := m.rpcEndpoints
	stats := m.rpcStatsProvider
	closed := m.closed || m.loading || m.unloading
	m.mu.Unlock()
	if closed {
		return nil, errors.New("manager closed")
	}
	var fixed *benchmarkPlacement
	if rounds > 1 {
		fixed = &benchmarkPlacement{}
	}
	rows := make([]benchmarkRound, 0, rounds)
	snapshot := func() map[string]rpcmesh.TunnelStats {
		if stats == nil {
			return nil
		}
		return stats()
	}
	for i := range rounds {
		roundCtx := context.WithValue(ctx, benchmarkRoundKey{}, [2]int{i + 1, rounds})
		benchmarkPhase(roundCtx, "round_start", "")
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if fixed != nil && fixed.ready {
			// Native llama-server closes its RPC connections at the end of each
			// round. The Guest may need a moment to replenish idle tunnels.
			// Preserve the exact authenticated device identity and placement;
			// do not misclassify a transient busy tunnel as topology drift.
			benchmarkPhase(roundCtx, "reconnect", "")
			if err := fixed.waitForRPCIdle(ctx, provider); err != nil {
				return nil, err
			}
		}
		row := benchmarkRound{RPCStatsBefore: snapshot()}
		sample, err := m.measureInferenceDuration(roundCtx, settings, model, nodeID, extended, provider, duration, fixed)
		if err != nil {
			return nil, err
		}
		row.Inference, row.RPCStatsAfter = sample, snapshot()
		if len(rows) > 0 && (sample.BenchmarkTopology != rows[0].Inference.BenchmarkTopology || sample.RateBasis != rows[0].Inference.RateBasis || sample.Fingerprint != rows[0].Inference.Fingerprint) {
			return nil, errors.New("benchmark topology, rate basis or fingerprint changed between rounds")
		}
		rows = append(rows, row)
		benchmarkMeasured(roundCtx, "round_done", "", i+1, rounds, sample)
	}
	benchmarkPhase(ctx, "validate", "")
	sample := rows[len(rows)-1].Inference
	key := sample.Model
	m.mu.Lock()
	defer m.mu.Unlock()
	if sample.Fingerprint != m.benchmarkSignature(m.settings, model) {
		return nil, errors.New("runtime settings changed during benchmark")
	}
	if duration > 0 {
		return rows, nil // Stability is not a placement benchmark.
	}
	if _, exists := m.samples[key]; !exists {
		m.pruneSamplesLocked(time.Now())
	}
	m.samples[key] = sample
	if nodeID == "cluster" {
		m.microbenchDrift = 0
	}
	return rows, m.persistBenchmarksLocked()
}

func (m *Manager) measureInference(ctx context.Context, settings Settings, model, nodeID string, extended bool, provider func() []RPCGuest, frozen ...*benchmarkPlacement) (InferenceSample, error) {
	return m.measureInferenceDuration(ctx, settings, model, nodeID, extended, provider, 0, frozen...)
}

func (m *Manager) measureInferenceDuration(ctx context.Context, settings Settings, model, nodeID string, extended bool, provider func() []RPCGuest, duration time.Duration, frozen ...*benchmarkPlacement) (InferenceSample, error) {
	benchmarkPhase(ctx, "inspect", "")
	if settings.VisionProjector != "" {
		return InferenceSample{}, errors.New("text inference benchmarks require vision projector disabled")
	}
	settings.Model = model
	path, err := modelPath(m.modelDir, model)
	if err != nil {
		return InferenceSample{}, err
	}
	size, err := local.ModelSize(path)
	if err != nil {
		return InferenceSample{}, err
	}
	meta, err := m.inspectInferenceGGUF(path, settings)
	if err != nil {
		return InferenceSample{}, err
	}
	if meta.Trellis {
		if nodeID != "" {
			return InferenceSample{}, errors.New("Trellis/Gyro runtime is Host-only; select Host local for the benchmark")
		}
		settings = trellisHostSettings(settings)
	}
	benchmarkPhase(ctx, "memory", fmt.Sprintf("%d blocks; %.2f GiB", meta.BlockCount, float64(size)/(1<<30)))
	hostBudget, err := m.hostBudget(settings, hardware.Collect("Host", nil))
	if err != nil {
		return InferenceSample{}, err
	}
	if nodeID != "" && settings.ParallelSlots > 1 {
		return InferenceSample{}, errors.New("RPC benchmark requires a single inference slot")
	}
	if nodeID == "" {
		if modelExceedsBudget(uint64(size), hostBudget) {
			return InferenceSample{}, errors.New("model weights exceed Host memory budget")
		}
		if err = validatePlacementMemory(meta, settings, uint64(size), hostBudget, nil); err != nil {
			return InferenceSample{}, err
		}
	}
	benchmarkPhase(ctx, "runtime", "")
	binary := m.binary
	if meta.Trellis {
		binary, err = llamaruntime.EnsureTrellis(ctx, m.dir, settings.HostGPU)
		if err == nil {
			benchmarkPhase(ctx, "runtime", "resolving PLE dependencies")
			_, err = local.EnsureTrellisPLE(ctx, path, meta, nil)
		}
	} else if local.EffectiveBackend(settings.Backend) == local.BackendMistralRS {
		if nodeID != "" {
			return InferenceSample{}, errors.New("mistral.rs benchmark cannot target NodexLM RPC Guests")
		}
		binary = ""
	} else if nodeID != "" {
		if binary == "" || binary == "llama-server" {
			binary, _, err = llamaruntime.EnsureDistributed(ctx, m.dir, effectiveHostRuntimeBackend(settings.HostGPU))
		}
	} else {
		if (binary == "" || binary == "llama-server") && settings.HostGPU == "cuda" {
			binary, _, err = llamaruntime.EnsureDistributed(ctx, m.dir, "cuda")
		} else if (binary == "" || binary == "llama-server") && (settings.HostGPU == "vulkan" || settings.HostGPU == "metal") {
			binary, _, err = llamaruntime.Ensure(ctx, m.dir, settings.HostGPU)
		} else {
			binary, err = llamaruntime.AutoExecutable(ctx, m.dir, binary)
		}
	}
	if err != nil {
		return InferenceSample{}, err
	}
	if settings.HostGPU == "cuda" && settings.GPULayers != "0" && discoverHostGPU(ctx, binary, "cuda") == "" {
		return InferenceSample{}, errors.New("explicit CUDA requires exactly one initialized native CUDA device")
	}
	draftPath, err := m.resolveDraftModel(settings)
	if err != nil {
		return InferenceSample{}, err
	}
	engine := managedFromSettings(settings, binary, path, model, draftPath)
	engine.UnboundedStartup = true
	engine.TrellisRuntime = meta.Trellis
	var topology string
	var loadGuests []RPCGuest
	benchmarkPhase(ctx, "placement", "")
	if nodeID == "cluster" {
		if provider == nil {
			return InferenceSample{}, errors.New("no RPC Guests")
		}
		guests := provider()
		var selection clusterplan.Selection
		var placement RPCDevicePlacement
		var layers int
		var fixed *benchmarkPlacement
		if len(frozen) > 0 {
			fixed = frozen[0]
		}
		if fixed != nil && fixed.ready {
			if err := fixed.validate(guests, meta, settings, uint64(size), hostBudget); err != nil {
				return InferenceSample{}, err
			}
			selection, placement, layers = fixed.selection, fixed.devices, fixed.localLayers
			if placement.Modes["host"] != "" && (len(placement.Devices) == 0 || discoverHostGPU(ctx, binary, settings.HostGPU) != placement.Devices[0]) {
				return InferenceSample{}, errors.New("fixed Host GPU is no longer available")
			}
		} else {
			selection, err = m.chooseRPC(settings, meta, uint64(size), hostBudget, guests)
			if err != nil {
				return InferenceSample{}, err
			}
			if len(selection.Guests) == 0 {
				return InferenceSample{}, errors.New("placement selected Host only; choose capacity strategy to measure a cluster")
			}
			placement, layers = planRPCDevices(selection, guests, meta, settings, uint64(size)), selection.LocalLayers
			if len(placement.Devices) > 0 && layers > 0 && settings.GPULayers != "0" {
				device := discoverHostGPU(ctx, binary, settings.HostGPU)
				placement, layers = withHostGPU(placement, layers, meta.BlockCount, settings.HostGPU, device, settings.GPULayers)
			}
			if fixed != nil {
				*fixed = benchmarkPlacement{ready: true, selection: selection, devices: placement, localLayers: layers, guests: slices.Clone(guests)}
			}
		}
		for _, selected := range selection.Guests {
			for _, guest := range guests {
				if selected.ID == guest.ID {
					engine.RPCServers = append(engine.RPCServers, guest.Endpoint)
					loadGuests = append(loadGuests, guest)
					topology += guest.ID + ":"
					break
				}
			}
		}
		if len(engine.RPCServers) != len(selection.Guests) {
			return InferenceSample{}, errors.New("RPC placement changed")
		}
		benchmarkPhase(ctx, "preflight", fmt.Sprintf("%d Guest(s)", len(selection.Guests)))
		if err = preflightRPCGuests(ctx, selection.Guests, engine.RPCServers, guests...); err != nil {
			return InferenceSample{}, err
		}
		engine.RPCLocalLayers = layers
		engine.RPCDeviceNames = placement.Devices
		engine.RPCTensorSplit = placement.NativeTensorSplitArg()
		engine.GPULayers = "all"
		topology += fmt.Sprint(layers) + ":" + placement.DeviceArg() + ":" + placement.NativeTensorSplitArg()
	} else if nodeID != "" {
		if provider == nil {
			return InferenceSample{}, errors.New("no RPC Guests")
		}
		found := false
		for _, guest := range provider() {
			if guest.ID == nodeID {
				if modelExceedsBudget(uint64(size), guest.CapacityBytes) {
					return InferenceSample{}, errors.New("benchmark model exceeds Guest memory budget")
				}
				if err = validatePlacementMemory(meta, settings, uint64(size), hostBudget, []clusterplan.Candidate{{ID: guest.ID, CapacityBytes: guest.CapacityBytes}}); err != nil {
					return InferenceSample{}, err
				}
				engine.RPCServers = []string{guest.Endpoint}
				loadGuests = append(loadGuests, guest)
				benchmarkPhase(ctx, "preflight", "1 Guest")
				topology = guest.ID
				if err = preflightRPCGuests(ctx, []clusterplan.Candidate{{ID: guest.ID, CapacityBytes: guest.CapacityBytes}}, engine.RPCServers, guest); err != nil {
					return InferenceSample{}, err
				}
				engine.RPCLocalLayers = 0
				if settings.RPCNativeScheduler && !validRPCDeviceKinds(guest) {
					return InferenceSample{}, errors.New("native scheduler requires verified RPC devices")
				}
				if validRPCDeviceKinds(guest) {
					plan := planRPCDevices(clusterplan.Selection{Guests: []clusterplan.Candidate{{ID: guest.ID, CapacityBytes: guest.CapacityBytes}}}, []RPCGuest{guest}, meta, settings, uint64(size))
					if len(plan.Devices) == 0 {
						return InferenceSample{}, errors.New("model does not fit individual CPU/GPU budgets")
					}
					engine.RPCDeviceNames, engine.RPCTensorSplit = plan.Devices, plan.NativeTensorSplitArg()
					topology += ":" + plan.DeviceArg() + ":" + plan.NativeTensorSplitArg()
				}
				engine.GPULayers = "all"
				found = true
				break
			}
		}
		if !found {
			return InferenceSample{}, errors.New("requested Guest is not RPC ready")
		}
	}
	defer func() {
		benchmarkPhase(ctx, "cleanup", "")
		engine.Close()
	}()
	m.benchmarkLoadProgress(ctx, loadGuests, size)
	loadStart := time.Now()
	if err := engine.Load(ctx); err != nil {
		return InferenceSample{}, err
	}
	loadMilliseconds := float64(time.Since(loadStart)) / float64(time.Millisecond)
	cache := false
	request := protocol.ChatRequest{Model: model, Messages: []protocol.Message{
		{Role: "user", Content: "Write a short story about a small robot exploring a quiet forest."},
	}, MaxTokens: 64, Temperature: 0, CachePrompt: &cache}
	// An unmeasured warmup avoids treating first-use backend initialization as
	// steady-state decode; keep the same request for comparable model runs.
	benchmarkPhase(ctx, "warmup", fmt.Sprintf("%.1f s load", loadMilliseconds/1000))
	warmup := request
	warmup.MaxTokens = 8
	if _, err := engine.Complete(ctx, warmup); err != nil {
		return InferenceSample{}, err
	}
	const runs = 3
	measurements := make([]InferenceSample, 0, runs)
	key := model
	if nodeID != "" {
		if nodeID == "cluster" {
			key += "@bench-cluster"
		} else {
			key += "@" + nodeID
		}
	}
	if duration > 0 {
		sample, err := measureStability(ctx, engine, key, request, duration)
		if err != nil {
			return InferenceSample{}, err
		}
		sample.BenchmarkTopology = topology
		sample.LoadMilliseconds = loadMilliseconds
		sample.Fingerprint = m.benchmarkSignature(settings, model)
		return sample, nil
	}
	for i := 0; i < runs; i++ {
		benchmarkProgress(ctx, benchmarkProgressEvent{Phase: "measure", Current: i, Total: runs})
		start := time.Now()
		raw, err := engine.Complete(ctx, request)
		if err != nil {
			return InferenceSample{}, err
		}
		sample, ok := parseInferenceSample(key, raw, time.Since(start))
		if !ok {
			return InferenceSample{}, errors.New("backend did not report a completion token count")
		}
		if len(measurements) > 0 && sample.RateBasis != measurements[0].RateBasis {
			return InferenceSample{}, errors.New("benchmark runtime timing basis changed between runs")
		}
		measurements = append(measurements, sample)
		benchmarkMeasured(ctx, "sample", "", i+1, runs, sample)
	}
	// A median resists a single noisy network, thermal or scheduling outlier.
	sample := medianInferenceSample(slices.Clone(measurements))
	sample.Measurements = measurements
	if extended {
		sample.Workloads = benchmarkWorkloads(ctx, engine, key, request, settings.ContextTokens)
		if ctx.Err() != nil {
			return InferenceSample{}, ctx.Err()
		}
	}
	sample.NativeSystemInfo = engine.SystemInfo()
	sample.BenchmarkTopology = topology
	sample.LoadMilliseconds = loadMilliseconds
	sample.Kind = "benchmark"
	sample.Runs = runs
	sample.MeasuredAt = time.Now()
	sample.Fingerprint = m.benchmarkSignature(settings, model)
	if sample.Fingerprint == "" {
		return InferenceSample{}, errors.New("model changed during benchmark")
	}
	return sample, nil
}

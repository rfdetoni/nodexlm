package dashboard

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// BenchmarkInference runs the same bounded text workload locally or on one
// authenticated RPC Guest with the configured context and runtime options.
// Report decode measurements separately from live end-to-end timings.
// It must run with resident models unloaded, avoiding an undisclosed eviction.
func (m *Manager) BenchmarkInference(ctx context.Context, model, nodeID string) (InferenceSample, error) {
	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.mu.Lock()
	m.poolCancel = cancel
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.poolCancel = nil; m.mu.Unlock() }()
	if len(m.LoadedModels()) != 0 {
		return InferenceSample{}, errors.New("unload resident models before inference benchmark")
	}
	m.mu.Lock()
	settings := m.settings
	provider := m.rpcEndpoints
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return InferenceSample{}, errors.New("manager closed")
	}
	path, err := modelPath(m.modelDir, model)
	if err != nil {
		return InferenceSample{}, err
	}
	size, err := local.ModelSize(path)
	if err != nil {
		return InferenceSample{}, err
	}
	binary := m.binary
	if local.EffectiveBackend(settings.Backend) == local.BackendMistralRS {
		if nodeID != "" {
			return InferenceSample{}, errors.New("mistral.rs benchmark cannot target NodexLM RPC Guests")
		}
		binary = ""
	} else if nodeID != "" {
		if binary == "" || binary == "llama-server" {
			binary, _, err = llamaruntime.EnsureDistributed(ctx, m.dir, "auto")
		}
	} else {
		binary, err = llamaruntime.AutoExecutable(ctx, m.dir, binary)
	}
	if err != nil {
		return InferenceSample{}, err
	}
	draftPath, err := m.resolveDraftModel(settings)
	if err != nil {
		return InferenceSample{}, err
	}
	engine := managedFromSettings(settings, binary, path, model, draftPath)
	if nodeID != "" {
		if provider == nil {
			return InferenceSample{}, errors.New("no RPC Guests")
		}
		found := false
		for _, guest := range provider() {
			if guest.ID == nodeID {
				if modelExceedsBudget(uint64(size), guest.CapacityBytes) {
					return InferenceSample{}, errors.New("benchmark model exceeds Guest memory budget")
				}
				engine.RPCServers = []string{guest.Endpoint}
				engine.RPCLocalLayers = 0
				engine.GPULayers = "all"
				found = true
				break
			}
		}
		if !found {
			return InferenceSample{}, errors.New("requested Guest is not RPC ready")
		}
	}
	defer engine.Close()
	if err := engine.Load(ctx); err != nil {
		return InferenceSample{}, err
	}
	cache := false
	request := protocol.ChatRequest{Model: model, Messages: []protocol.Message{
		{Role: "user", Content: "Write a short story about a small robot exploring a quiet forest."},
	}, MaxTokens: 64, Temperature: 0, CachePrompt: &cache}
	// An unmeasured warmup avoids treating first-use backend initialization as
	// steady-state decode; keep the same request for comparable model runs.
	warmup := request
	warmup.MaxTokens = 8
	if _, err := engine.Complete(ctx, warmup); err != nil {
		return InferenceSample{}, err
	}
	const runs = 3
	measurements := make([]InferenceSample, 0, runs)
	key := model
	if nodeID != "" { key += "@" + nodeID }
	for i := 0; i < runs; i++ {
		start := time.Now()
		raw, err := engine.Complete(ctx, request)
		if err != nil { return InferenceSample{}, err }
		sample, ok := parseInferenceSample(key, raw, time.Since(start))
		if !ok { return InferenceSample{}, errors.New("backend did not report a completion token count") }
		if len(measurements) > 0 && sample.RateBasis != measurements[0].RateBasis {
			return InferenceSample{}, errors.New("benchmark runtime timing basis changed between runs")
		}
		measurements = append(measurements, sample)
	}
	// A median resists a single noisy network, thermal or scheduling outlier.
	sort.Slice(measurements, func(i, j int) bool {
		return measurements[i].TokensPerSecond < measurements[j].TokensPerSecond
	})
	sample := measurements[runs/2]
	sample.Kind = "benchmark"
	sample.Runs = runs
	sample.MeasuredAt = time.Now()
	sample.Fingerprint = m.benchmarkSignature(settings, model)
	if sample.Fingerprint == "" { return InferenceSample{}, errors.New("model changed during benchmark") }
	m.mu.Lock()
	if sample.Fingerprint != m.benchmarkSignature(m.settings, model) {
		m.mu.Unlock()
		return InferenceSample{}, errors.New("runtime settings changed during benchmark")
	}
	if _, exists := m.samples[key]; !exists { m.pruneSamplesLocked(time.Now()) }
	m.samples[key] = sample
	err = m.persistBenchmarksLocked()
	m.mu.Unlock()
	return sample, err
}

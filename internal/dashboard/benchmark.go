package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// BenchmarkInference runs the same bounded text workload locally or on one
// authenticated RPC Guest, keeping output/embedding tensors on the Host.
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
	if nodeID != "" {
		if binary == "" || binary == "llama-server" {
			binary, _, err = llamaruntime.EnsureDistributed(ctx, m.dir, "auto")
		}
	} else {
		binary, err = llamaruntime.AutoExecutable(ctx, m.dir, binary)
	}
	if err != nil {
		return InferenceSample{}, err
	}
	engine := &local.Managed{Binary: binary, ModelPath: path, Alias: model, Threads: settings.Threads, ContextTokens: 512, GPULayers: settings.GPULayers, FlashAttention: settings.FlashAttention, KVCacheType: settings.KVCacheType, BatchSize: settings.BatchSize, UBatchSize: settings.UBatchSize, SpeculativeMode: settings.SpeculativeMode, KeepLoaded: true}
	if nodeID != "" {
		if provider == nil {
			return InferenceSample{}, errors.New("no RPC Guests")
		}
		found := false
		for _, guest := range provider() {
			if guest.ID == nodeID {
				if uint64(size) > guest.CapacityBytes/115*100 {
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
	start := time.Now()
	raw, err := engine.Complete(ctx, protocol.ChatRequest{Model: model, Messages: []protocol.Message{{Role: "user", Content: "Write a short story about a small robot exploring a quiet forest."}}, MaxTokens: 64, Temperature: 0, CachePrompt: &cache})
	if err != nil {
		return InferenceSample{}, err
	}
	key := model
	if nodeID != "" {
		key = model + "@" + nodeID
	}
	m.recordInference(key, raw, time.Since(start))
	m.mu.Lock()
	sample, ok := m.samples[key]
	m.mu.Unlock()
	if !ok {
		return sample, errors.New("backend did not report a completion token count")
	}
	// Validate the response fully before reporting a successful benchmark.
	var completion map[string]json.RawMessage
	if err := json.Unmarshal(raw, &completion); err != nil {
		return sample, err
	}
	return sample, nil
}

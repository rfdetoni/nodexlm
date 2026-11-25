package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/local"
)

type TuneCandidate struct {
	Name     string           `json:"name"`
	Settings Settings         `json:"settings"`
	Sample   *InferenceSample `json:"sample,omitempty"`
	Error    string           `json:"error,omitempty"`
}

type TuneReport struct {
	Fingerprint string          `json:"fingerprint"`
	MeasuredAt  time.Time       `json:"measured_at"`
	Candidates  []TuneCandidate `json:"candidates"`
	Recommended *Settings       `json:"recommended,omitempty"`
}

func tuneCandidates(s Settings, physical int, slowRPC bool) []TuneCandidate {
	physical = min(256, max(1, physical))
	rows := []TuneCandidate{{Name: "baseline", Settings: s}}
	add := func(name string, candidate Settings) {
		for _, row := range rows {
			if row.Settings == candidate {
				return
			}
		}
		rows = append(rows, TuneCandidate{Name: name, Settings: candidate})
	}
	for _, threads := range []int{max(1, physical/2), max(1, physical-2), physical} {
		candidate := s
		candidate.Threads, candidate.ThreadsBatch = threads, physical
		add("cpu", candidate)
	}
	for _, ubatch := range []int{512, 1024} {
		candidate := s
		candidate.UBatchSize = ubatch
		candidate.BatchSize = max(ubatch, s.BatchSize)
		add("prefill", candidate)
	}
	// N-gram is measured against the same workload without downloading or
	// guessing a compatible draft model. Recommendations never silently apply.
	if slowRPC {
		for _, tokens := range []int{4, 8, 16} {
			candidate := s
			candidate.SpeculativeMode, candidate.DraftModel = "ngram", ""
			candidate.SpeculativeTokens = tokens
			candidate.BatchSize = max(s.BatchSize, s.UBatchSize)
			add("ngram", candidate)
		}
	} else if s.SpeculativeMode == "" || s.SpeculativeMode == "off" {
		candidate := s
		candidate.SpeculativeMode = "ngram"
		add("ngram", candidate)
	}
	return rows
}

// A controlled cluster benchmark records selected Guest IDs as a prefix of
// BenchmarkTopology. Single-node benchmarks record only the Guest ID.
func (m *Manager) slowBenchmarkRPC(topology string) bool {
	for _, id := range strings.Split(topology, ":") {
		if len(id) != 64 {
			break
		}
		if network := m.networkSample(id); network != nil && network.RoundTripMS >= speculativeRTTThresholdMS {
			return true
		}
	}
	return false
}

// TuneInference owns the same model pool lock as load/benchmark and uses
// one runtime at a time. It never writes runtime.json or changes user settings.
func (m *Manager) TuneInference(ctx context.Context, model string) (TuneReport, error) {
	return m.tuneInferenceTarget(ctx, model, "")
}

func (m *Manager) tuneInferenceTarget(ctx context.Context, model, node string) (TuneReport, error) {
	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.mu.Lock()
	if m.closed || m.loading || m.unloading || m.engine != nil || len(m.residents) > 0 {
		m.mu.Unlock()
		return TuneReport{}, errors.New("unload all models before autotuning")
	}
	s := m.settings
	provider := m.rpcEndpoints
	m.poolCancel = cancel
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.poolCancel = nil; m.mu.Unlock() }()
	if s.ParallelSlots > 1 || local.EffectiveBackend(s.Backend) != local.BackendLlama {
		return TuneReport{}, errors.New("autotuning requires one llama.cpp slot")
	}
	s.Model = model
	fingerprint := m.benchmarkSignature(s, model)
	if fingerprint == "" {
		return TuneReport{}, errors.New("invalid model for autotuning")
	}
	report := TuneReport{Fingerprint: fingerprint, MeasuredAt: time.Now()}
	measure := func(row TuneCandidate) {
		sample, err := m.measureInference(ctx, row.Settings, model, node, false, provider)
		if err != nil {
			row.Error = err.Error()
		} else {
			row.Sample = &sample
		}
		report.Candidates = append(report.Candidates, row)
	}
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	measure(TuneCandidate{Name: "baseline", Settings: s})
	first := report.Candidates[0].Sample
	slowRPC := node != "" && first != nil && m.slowBenchmarkRPC(first.BenchmarkTopology)
	for _, row := range tuneCandidates(s, hardware.PhysicalCPUCount(), slowRPC)[1:] {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		measure(row)
	}
	// Prefer a real decode gain with no material prefill regression, not an
	// invented aggregate score. Require matching measurement basis.
	baseline := report.Candidates[0].Sample
	if baseline != nil && baseline.RateBasis == "native_decode" {
		best := baseline.TokensPerSecond * 1.05
		for _, row := range report.Candidates[1:] {
			sample := row.Sample
			if sample == nil || sample.RateBasis != baseline.RateBasis || sample.BenchmarkTopology != baseline.BenchmarkTopology || sample.TokensPerSecond <= best {
				continue
			}
			if baseline.PrefillTokensPerSecond > 0 && sample.PrefillTokensPerSecond < baseline.PrefillTokensPerSecond*0.9 {
				continue
			}
			if row.Settings.SpeculativeMode == "ngram" && (sample.DraftAcceptance == nil || *sample.DraftAcceptance < 0.25) {
				continue
			}
			best = sample.TokensPerSecond
			candidate := row.Settings
			report.Recommended = &candidate
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if fingerprint != m.benchmarkSignature(m.settings, model) {
		return report, errors.New("settings changed during autotuning")
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return report, err
	}
	filename := "autotune.json"
	if node != "" {
		// Hash the target instead of using an untrusted node ID as a path.
		filename = fmt.Sprintf("autotune-%x.json", sha256.Sum256([]byte(node)))
	}
	return report, writePrivate(filepath.Join(m.dir, filename), raw)
}

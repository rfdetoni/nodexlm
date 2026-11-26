package dashboard

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

type InferenceSample struct {
	Model           string    `json:"model"`
	MeasuredAt      time.Time `json:"measured_at"`
	Tokens          int       `json:"tokens"`
	PromptTokens    int       `json:"prompt_tokens"`
	CachedTokens    int       `json:"cached_tokens"`
	Seconds         float64   `json:"seconds"`
	TokensPerSecond float64   `json:"tokens_per_second"`
	// benchmark = identical fixed prompt/output run on Host or a Guest.
	// live measurements must never be used to select faster hardware.
	Kind string `json:"kind,omitempty"`
	// native_decode uses the llama.cpp predicted_ms timing. When absent,
	// rate includes prefill and first-token latency (end_to_end).
	RateBasis string `json:"rate_basis,omitempty"`
	// PromptMilliseconds is the native prefill timing, not an invented TTFT.
	PromptMilliseconds         float64  `json:"prompt_ms,omitempty"`
	PrefillTokensPerSecond     float64  `json:"prefill_tokens_per_second,omitempty"`
	DraftTokens                int      `json:"draft_tokens,omitempty"`
	DraftAccepted              int      `json:"draft_accepted,omitempty"`
	DraftAcceptance            *float64 `json:"draft_acceptance,omitempty"`
	LoadMilliseconds           float64  `json:"load_ms,omitempty"`
	FirstOutputMilliseconds    float64  `json:"first_output_ms,omitempty"`
	FirstTextMilliseconds      *float64 `json:"first_text_ms,omitempty"`
	FirstReasoningMilliseconds *float64 `json:"first_reasoning_ms,omitempty"`

	ReasoningTokens    *int `json:"reasoning_tokens,omitempty"`
	NonReasoningTokens *int `json:"non_reasoning_tokens,omitempty"`
	// SSE is not a per-token clock. These optional throughput values use
	// server-reported token counts and observed reasoning/text event windows.
	ObservedReasoningTokensPerSecond *float64 `json:"observed_reasoning_tokens_per_second,omitempty"`
	ObservedAnswerTokensPerSecond    *float64 `json:"observed_answer_tokens_per_second,omitempty"`
	PhaseRateBasis                   string   `json:"phase_rate_basis,omitempty"`

	OutputChunkP50Milliseconds float64              `json:"output_chunk_p50_ms,omitempty"`
	OutputChunkP95Milliseconds float64              `json:"output_chunk_p95_ms,omitempty"`
	Workloads                  []BenchmarkWorkload  `json:"workloads,omitempty"`
	ElapsedMilliseconds        float64              `json:"elapsed_ms,omitempty"`
	Runs                       int                  `json:"runs,omitempty"`
	Fingerprint                string               `json:"fingerprint,omitempty"`
	NativeSystemInfo           string               `json:"native_system_info,omitempty"`
	BenchmarkTopology          string               `json:"benchmark_topology,omitempty"`
	Suspect                    bool                 `json:"suspect,omitempty"`
	RPCTraffic                 *InferenceRPCTraffic `json:"rpc_traffic,omitempty"`
}

// parseInferenceSample is shared by live metrics and controlled benchmarks.
func parseInferenceSample(model string, raw []byte, elapsed time.Duration) (InferenceSample, bool) {
	var out struct {
		Usage struct {
			Completion        int `json:"completion_tokens"`
			Output            int `json:"output_tokens"`
			Input             int `json:"input_tokens"`
			Prompt            int `json:"prompt_tokens"`
			CompletionDetails struct {
				Reasoning *int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
			OutputDetails struct {
				Reasoning *int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
			Details struct {
				Cached int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Timings struct {
			Tokens        int     `json:"predicted_n"`
			Milliseconds  float64 `json:"predicted_ms"`
			PromptMS      float64 `json:"prompt_ms"`
			PromptN       int     `json:"prompt_n"`
			PromptRate    float64 `json:"prompt_per_second"`
			DraftN        int     `json:"draft_n"`
			DraftAccepted int     `json:"draft_n_accepted"`
		} `json:"timings"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return InferenceSample{}, false
	}
	tokens := out.Usage.Completion
	if tokens == 0 {
		tokens = out.Usage.Output
	}
	if tokens == 0 {
		tokens = out.Timings.Tokens
	}
	if tokens <= 0 || tokens > 1<<20 {
		return InferenceSample{}, false
	}
	prompt := out.Usage.Prompt
	if prompt == 0 {
		prompt = out.Usage.Input
	}
	seconds := elapsed.Seconds()
	rateTokens := tokens
	basis := "end_to_end"
	if out.Timings.Milliseconds > 0 {
		seconds = out.Timings.Milliseconds / 1000
		basis = "native_decode"
		if out.Timings.Tokens > 0 && out.Timings.Tokens <= 1<<20 {
			rateTokens = out.Timings.Tokens
		}
	}
	if seconds <= 0 {
		return InferenceSample{}, false
	}
	prefill := out.Timings.PromptRate
	if prefill <= 0 && out.Timings.PromptN > 0 && out.Timings.PromptMS > 0 {
		prefill = float64(out.Timings.PromptN) * 1000 / out.Timings.PromptMS
	}
	var acceptance *float64
	if out.Timings.DraftN > 0 && out.Timings.DraftAccepted >= 0 && out.Timings.DraftAccepted <= out.Timings.DraftN {
		rate := float64(out.Timings.DraftAccepted) / float64(out.Timings.DraftN)
		acceptance = &rate
	}
	// Reasoning is a subset of completion/output tokens, never an extra count.
	var reasoning, nonReasoning *int
	reported, reportedTotal := out.Usage.CompletionDetails.Reasoning, out.Usage.Completion
	if reported == nil && out.Usage.Completion == 0 {
		reported, reportedTotal = out.Usage.OutputDetails.Reasoning, out.Usage.Output
	}
	if reported != nil && reportedTotal > 0 && *reported >= 0 && *reported <= reportedTotal {
		remaining := reportedTotal - *reported
		reasoning, nonReasoning = reported, &remaining
	}
	return InferenceSample{
		ReasoningTokens: reasoning, NonReasoningTokens: nonReasoning,
		PrefillTokensPerSecond: prefill, DraftTokens: out.Timings.DraftN,
		DraftAccepted: out.Timings.DraftAccepted, DraftAcceptance: acceptance,
		Model: model, MeasuredAt: time.Now(), Tokens: tokens, PromptTokens: prompt,
		CachedTokens: out.Usage.Details.Cached, Seconds: seconds,
		TokensPerSecond: float64(rateTokens) / seconds,
		Kind:            "live", RateBasis: basis, PromptMilliseconds: out.Timings.PromptMS,
		ElapsedMilliseconds: elapsed.Seconds() * 1000,
	}, true
}

// pruneSamplesLocked keeps a small, deterministic retention policy, preferring
// live measurements for eviction before valid benchmark comparisons.
func (m *Manager) pruneSamplesLocked(now time.Time) {
	for key, sample := range m.samples {
		if sample.MeasuredAt.IsZero() {
			continue
		}
		age := now.Sub(sample.MeasuredAt)
		ttl := time.Hour
		if sample.Kind == "benchmark" {
			ttl = 24 * time.Hour
		}
		if age < 0 || age >= ttl {
			delete(m.samples, key)
		}
	}
	if len(m.samples) < 32 {
		return
	}
	var oldest string
	for key, sample := range m.samples {
		if oldest == "" {
			oldest = key
			continue
		}
		current := m.samples[oldest]
		if (sample.Kind != "benchmark" && current.Kind == "benchmark") ||
			(sample.Kind == current.Kind && (sample.MeasuredAt.Before(current.MeasuredAt) ||
				(sample.MeasuredAt.Equal(current.MeasuredAt) && key < oldest))) {
			oldest = key
		}
	}
	delete(m.samples, oldest)
}

func (m *Manager) recordInference(model string, raw []byte, elapsed time.Duration) {
	m.recordInferenceWithLatency(model, raw, elapsed, streamLatency{})
}

func (m *Manager) recordInferenceWithLatency(model string, raw []byte, elapsed time.Duration, latency streamLatency) {
	sample, ok := parseInferenceSample(model, raw, elapsed)
	if !ok {
		return
	}
	latency.apply(&sample)
	m.mu.Lock()
	// A live distributed result is not evidence of Host-only performance.
	if m.engine != nil {
		sample.NativeSystemInfo = m.nativeSystemInfo
		if len(m.engine.RPCServers) > 0 {
			sample.Model += "@cluster"
			// Only compare the controlled cluster sample with equivalent
			// native decode measurements, never a single Guest's rate.
			key := model + "@bench-cluster"
			topology := strings.Join(m.loadRPCNodes, ":") + ":" + fmt.Sprint(m.engine.RPCLocalLayers) + ":" + strings.Join(m.engine.RPCDeviceNames, ",") + ":" + m.engine.RPCTensorSplit
			if baseline, ok := m.samples[key]; ok && baseline.Kind == "benchmark" &&
				model == m.current && m.loadFingerprint != "" && baseline.Fingerprint == m.loadFingerprint &&
				baseline.BenchmarkTopology == topology && len(m.loadRPCNodes) > 0 &&
				baseline.RateBasis == "native_decode" && sample.RateBasis == baseline.RateBasis &&
				baseline.TokensPerSecond > 0 && !math.IsNaN(baseline.TokensPerSecond) && !math.IsInf(baseline.TokensPerSecond, 0) &&
				!baseline.MeasuredAt.After(time.Now()) && time.Since(baseline.MeasuredAt) < 24*time.Hour {
				if sample.TokensPerSecond < baseline.TokensPerSecond*0.7 || sample.TokensPerSecond > baseline.TokensPerSecond*1.3 {
					m.microbenchDrift++
				} else {
					m.microbenchDrift = 0
				}
				if m.microbenchDrift >= 3 {
					baseline.Suspect = true
					m.samples[key] = baseline
				}
			} else {
				m.microbenchDrift = 0
			}
		} else {
			sample.Model += "@live-host"
		}
	}
	if _, exists := m.samples[sample.Model]; !exists {
		m.pruneSamplesLocked(time.Now())
	}
	m.samples[sample.Model] = sample
	m.generationTokens += uint64(sample.Tokens)
	m.mu.Unlock()
}

func (m *Manager) InferenceSamples() []InferenceSample {
	m.mu.Lock()
	samples := []InferenceSample{}
	children := []*Manager{}
	for _, s := range m.samples {
		samples = append(samples, s)
	}
	for _, c := range m.residents {
		children = append(children, c)
	}
	m.mu.Unlock()
	for _, c := range children {
		samples = append(samples, c.InferenceSamples()...)
	}
	return samples
}

var generationBounds = [...]float64{0.1, 0.5, 1, 5, 10, 30, 60, 240}

func (m *Manager) recordGeneration(elapsed time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generationCount++
	m.generationSeconds += elapsed.Seconds()
	if err != nil {
		m.generationErrors++
	}
	for i, bound := range generationBounds {
		if elapsed.Seconds() <= bound {
			m.generationBuckets[i]++
		}
	}
}
func (m *Manager) WriteMetrics(w http.ResponseWriter) {
	m.mu.Lock()
	count, failures, seconds, tokens, buckets := m.generationCount, m.generationErrors, m.generationSeconds, m.generationTokens, m.generationBuckets
	m.mu.Unlock()
	fmt.Fprintf(w, "# TYPE nodexlm_host_generations_total counter\nnodexlm_host_generations_total %d\n# TYPE nodexlm_host_generation_errors_total counter\nnodexlm_host_generation_errors_total %d\n# TYPE nodexlm_host_generation_seconds histogram\nnodexlm_host_generation_seconds_count %d\nnodexlm_host_generation_seconds_sum %g\n", count, failures, count, seconds)
	fmt.Fprintf(w, "# TYPE nodexlm_host_reported_completion_tokens_total counter\nnodexlm_host_reported_completion_tokens_total %d\n", tokens)
	for i, bound := range generationBounds {
		fmt.Fprintf(w, "nodexlm_host_generation_seconds_bucket{le=%q} %d\n", fmt.Sprint(bound), buckets[i])
	}
	fmt.Fprintf(w, "nodexlm_host_generation_seconds_bucket{le=\"+Inf\"} %d\n", count)
	for _, s := range m.InferenceSamples() {
		if s.PrefillTokensPerSecond > 0 {
			fmt.Fprintf(w, "nodexlm_model_prefill_tokens_per_second{model=%q} %g\n", s.Model, s.PrefillTokensPerSecond)
		}
		if s.DraftAcceptance != nil {
			fmt.Fprintf(w, "nodexlm_model_draft_acceptance_ratio{model=%q} %g\n", s.Model, *s.DraftAcceptance)
		}
		fmt.Fprintf(w, "nodexlm_model_tokens_per_second{model=%q} %g\nnodexlm_model_cached_prompt_tokens{model=%q} %d\n", s.Model, s.TokensPerSecond, s.Model, s.CachedTokens)
	}
}

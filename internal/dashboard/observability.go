package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	Kind            string    `json:"kind,omitempty"`
	// native_decode uses the llama.cpp predicted_ms timing. When absent,
	// rate includes prefill and first-token latency (end_to_end).
	RateBasis       string    `json:"rate_basis,omitempty"`
}

func (m *Manager) recordInference(model string, raw []byte, elapsed time.Duration) {
	var out struct {
		Usage struct {
			Completion int `json:"completion_tokens"`
			Output     int `json:"output_tokens"`
			Input      int `json:"input_tokens"`
			Prompt     int `json:"prompt_tokens"`
			Details    struct {
				Cached int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Timings struct {
			Tokens       int     `json:"predicted_n"`
			Milliseconds float64 `json:"predicted_ms"`
		} `json:"timings"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return
	}
	tokens := out.Usage.Completion
	if tokens == 0 {
		tokens = out.Usage.Output
	}
	if out.Usage.Prompt == 0 {
		out.Usage.Prompt = out.Usage.Input
	}
	if tokens == 0 {
		tokens = out.Timings.Tokens
	}
	if tokens <= 0 || tokens > 1<<20 {
		return
	}
	seconds := elapsed.Seconds()
	if out.Timings.Milliseconds > 0 {
		seconds = out.Timings.Milliseconds / 1000
	}
	if seconds <= 0 {
		return
	}
	basis := "end_to_end"
	if out.Timings.Milliseconds > 0 {
		basis = "native_decode"
	}
	m.mu.Lock()
	// A live distributed result is not evidence of Host-only performance;
	// keep it distinct from local and per-Guest benchmark samples.
	if m.engine != nil && len(m.engine.RPCServers) > 0 {
		model += "@cluster"
	}
	sample := InferenceSample{Model: model, MeasuredAt: time.Now(), Tokens: tokens,
		PromptTokens: out.Usage.Prompt, CachedTokens: out.Usage.Details.Cached,
		Seconds: seconds, TokensPerSecond: float64(tokens) / seconds,
		Kind: "live", RateBasis: basis}
	if len(m.samples) >= 32 {
		for name := range m.samples {
			delete(m.samples, name)
			break
		}
	}
	m.samples[model] = sample
	m.generationTokens += uint64(tokens)
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
		fmt.Fprintf(w, "nodexlm_model_tokens_per_second{model=%q} %g\nnodexlm_model_cached_prompt_tokens{model=%q} %d\n", s.Model, s.TokensPerSecond, s.Model, s.CachedTokens)
	}
}

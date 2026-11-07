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
}

func (m *Manager) recordInference(model string, raw []byte, elapsed time.Duration) {
	var out struct {
		Usage struct {
			Completion int `json:"completion_tokens"`
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
	sample := InferenceSample{model, time.Now(), tokens, out.Usage.Prompt, out.Usage.Details.Cached, seconds, float64(tokens) / seconds}
	m.mu.Lock()
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

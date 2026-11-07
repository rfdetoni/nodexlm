package dashboard

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReportedTokensAndCumulativeLatencyBuckets(t *testing.T) {
	m := &Manager{samples: map[string]InferenceSample{}}
	raw := []byte(`{"usage":{"completion_tokens":8,"prompt_tokens":12,"prompt_tokens_details":{"cached_tokens":6}},"timings":{"predicted_ms":500}}`)
	m.recordInference("tiny.gguf", raw, time.Second)
	m.recordInference("tiny.gguf", raw, time.Second)
	m.recordGeneration(250*time.Millisecond, nil)
	m.recordGeneration(2*time.Second, nil)
	w := httptest.NewRecorder()
	m.WriteMetrics(w)
	for _, want := range []string{
		"nodexlm_host_reported_completion_tokens_total 16\n",
		"nodexlm_host_generation_seconds_bucket{le=\"0.1\"} 0\n",
		"nodexlm_host_generation_seconds_bucket{le=\"0.5\"} 1\n",
		"nodexlm_host_generation_seconds_bucket{le=\"5\"} 2\n",
		"nodexlm_host_generation_seconds_bucket{le=\"+Inf\"} 2\n",
		"nodexlm_model_tokens_per_second{model=\"tiny.gguf\"} 16\n",
	} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %q in metrics %s", want, w.Body.String())
		}
	}
}

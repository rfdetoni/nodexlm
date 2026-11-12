package dashboard

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/local"
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

func TestLiveDistributedSampleDoesNotPoisonHostBenchmark(t *testing.T) {
	m := &Manager{
		samples: map[string]InferenceSample{},
		engine:  &local.Managed{RPCServers: []string{"127.0.0.1:50052"}},
	}
	raw := []byte(`{"usage":{"completion_tokens":8},"timings":{"predicted_ms":16000}}`)
	m.recordInference("small.gguf", raw, 18*time.Second)
	if _, ok := m.samples["small.gguf"]; ok {
		t.Fatal("distributed decode was stored as a Host-only benchmark")
	}
	sample, ok := m.samples["small.gguf@cluster"]
	if !ok || sample.Kind != "live" || sample.RateBasis != "native_decode" ||
		sample.TokensPerSecond != 0.5 {
		t.Fatalf("unexpected distributed sample: %+v", sample)
	}
}

func TestLiveLocalSamplePreservesControlledHostBenchmark(t *testing.T) {
	m := &Manager{
		samples: map[string]InferenceSample{
			"small.gguf": {Model: "small.gguf", TokensPerSecond: 18, Kind: "benchmark"},
		},
		engine: &local.Managed{},
	}
	raw := []byte(`{"usage":{"completion_tokens":8},"timings":{"predicted_ms":16000}}`)
	m.recordInference("small.gguf", raw, 18*time.Second)
	if got := m.samples["small.gguf"]; got.Kind != "benchmark" || got.TokensPerSecond != 18 {
		t.Fatalf("live Host chat overwrote controlled benchmark: %+v", got)
	}
	if got := m.samples["small.gguf@live-host"]; got.TokensPerSecond != 0.5 || got.Kind != "live" {
		t.Fatalf("missing live Host metric: %+v", got)
	}
}

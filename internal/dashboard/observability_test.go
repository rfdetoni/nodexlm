package dashboard

import (
	"fmt"
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

func TestSampleCacheEvictsOldestLiveBeforeBenchmark(t *testing.T) {
	m := &Manager{samples: make(map[string]InferenceSample)}
	now := time.Now()
	m.samples["old-live"] = InferenceSample{Model: "old-live", Kind: "live", MeasuredAt: now.Add(-30 * time.Minute)}
	for i := 0; i < 31; i++ {
		key := fmt.Sprintf("bench-%d", i)
		m.samples[key] = InferenceSample{Model: key, Kind: "benchmark", MeasuredAt: now.Add(-time.Minute)}
	}
	m.recordInference("new-live", []byte(`{"usage":{"completion_tokens":8},"timings":{"predicted_ms":1000}}`), time.Second)
	if _, ok := m.samples["old-live"]; ok {
		t.Fatal("old live measurement was retained over benchmarks")
	}
	if _, ok := m.samples["new-live"]; !ok {
		t.Fatal("new live measurement was dropped")
	}
	if len(m.samples) != 32 {
		t.Fatalf("expected 32 samples, got %d", len(m.samples))
	}
}

func TestParseInferenceSampleKeepsPrefillSeparateFromDecode(t *testing.T) {
	got, ok := parseInferenceSample("m.gguf", []byte(`{"usage":{"completion_tokens":16},"timings":{"prompt_ms":2000,"predicted_ms":8000}}`), 11*time.Second)
	if !ok || got.RateBasis != "native_decode" || got.TokensPerSecond != 2 ||
		got.PromptMilliseconds != 2000 || got.ElapsedMilliseconds != 11000 {
		t.Fatalf("unreliable native timing breakdown: %+v valid=%v", got, ok)
	}
}

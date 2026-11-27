package dashboard

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

type BenchmarkWorkload struct {
	Profile string           `json:"profile"`
	Sample  *InferenceSample `json:"sample,omitempty"`
	Status  string           `json:"status"`
}

func runStreamingBenchmark(ctx context.Context, engine *local.Managed, key string, request protocol.ChatRequest) (InferenceSample, error) {
	request.StreamOptions = &protocol.StreamOptions{IncludeUsage: true}
	start := time.Now()
	latency := streamLatency{start: start}
	var usage []byte
	err := engine.GenerateStream(ctx, request, func(frame []byte) error {
		payload := bytes.TrimSpace(bytes.TrimPrefix(frame, []byte("data: ")))
		latency.observe(payload, time.Now())
		if bytes.Contains(payload, []byte(`"usage"`)) || bytes.Contains(payload, []byte(`"timings"`)) {
			if _, ok := parseInferenceSample(key, payload, time.Since(start)); ok {
				usage = append(usage[:0], payload...)
			}
		}
		return nil
	})
	if err != nil {
		return InferenceSample{}, err
	}
	sample, ok := parseInferenceSample(key, usage, time.Since(start))
	if !ok {
		return InferenceSample{}, errors.New("stream did not report completion token counts")
	}
	latency.apply(&sample)
	return sample, nil
}

// Extended workloads are opt-in. Token counts depend on the tokenizer;
// report actual prompt_tokens rather than asserting fixed token lengths.
func benchmarkWorkloads(ctx context.Context, engine *local.Managed, key string, request protocol.ChatRequest, contextTokens int) []BenchmarkWorkload {
	var result []BenchmarkWorkload
	for _, workload := range []struct {
		name                string
		repeats, minContext int
	}{{"short", 0, 256}, {"medium", 60, 2048}, {"long", 240, 8192}} {
		row := BenchmarkWorkload{Profile: workload.name, Status: "skipped_context"}
		benchmarkPhase(ctx, "context", workload.name)
		if contextTokens >= workload.minContext {
			req := request
			req.Messages = []protocol.Message{{Role: "user", Content: strings.Repeat("A small robot walks through a forest and records its observations. ", workload.repeats) + request.Messages[0].Content}}
			var samples []InferenceSample
			row.Status = "measured"
			for i := 0; i < 3; i++ {
				benchmarkProgress(ctx, benchmarkProgressEvent{Phase: "context", Detail: workload.name, Current: i, Total: 3})
				sample, err := runStreamingBenchmark(ctx, engine, key, req)
				if err != nil {
					row.Status = "unavailable"
					break
				}
				samples = append(samples, sample)
				benchmarkMeasured(ctx, "context_sample", workload.name, i+1, 3, sample)
			}
			if len(samples) == 3 {
				sample := medianInferenceSample(slices.Clone(samples))
				sample.Measurements = samples
				sample.Runs = 3
				row.Sample = &sample
			}
		}
		benchmarkPhase(ctx, "context_done", workload.name+" · "+row.Status)
		result = append(result, row)
		if ctx.Err() != nil {
			break
		}
	}
	return result
}

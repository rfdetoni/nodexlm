package dashboard

import (
	"context"
	"errors"
	"time"

	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// Keep one loaded runtime and issue sequential requests continuously. Retain
// at most one representative request per time window, not an unbounded trace.
func measureStability(ctx context.Context, engine *local.Managed, key string, request protocol.ChatRequest, duration time.Duration) (InferenceSample, error) {
	started := time.Now()
	deadline := started.Add(duration)
	benchmarkProgress(ctx, benchmarkProgressEvent{Phase: "stability", DurationMS: duration.Milliseconds()})
	var first, last InferenceSample
	var count, tokens int
	var elapsed float64
	var windows []InferenceSample
	nextWindow := started
	for time.Now().Before(deadline) {
		began := time.Now()
		// Let a request crossing the planned boundary finish. Slow inference
		// must not be interrupted by an arbitrary per-request deadline.
		raw, err := engine.Complete(ctx, request)
		if err != nil {
			return InferenceSample{}, err
		}
		sample, ok := parseInferenceSample(key, raw, time.Since(began))
		if !ok {
			return InferenceSample{}, errors.New("stability request lacks token count")
		}
		if count == 0 {
			first = sample
		} else if sample.RateBasis != first.RateBasis {
			return InferenceSample{}, errors.New("stability timing basis changed")
		}
		count++
		tokens += sample.Tokens
		elapsed += sample.Seconds
		last = sample
		if !began.Before(nextWindow) && len(windows) < 60 {
			windows = append(windows, sample)
			benchmarkMeasured(ctx, "stability_sample", "", count, 0, sample)
			nextWindow = began.Add(time.Minute)
		}
	}
	if count == 0 || elapsed <= 0 {
		return InferenceSample{}, errors.New("no stability samples")
	}
	result := last
	result.Kind = "stability"
	result.Tokens = tokens
	result.Seconds = time.Since(started).Seconds()
	result.ElapsedMilliseconds = float64(time.Since(started)) / float64(time.Millisecond)
	// Report aggregate wall-clock throughput, not native decode rate.
	result.TokensPerSecond = float64(tokens) * 1000 / result.ElapsedMilliseconds
	result.RateBasis = "end_to_end"
	result.PrefillTokensPerSecond = 0
	result.PromptMilliseconds = 0
	result.PromptTokens = 0
	result.CachedTokens = 0
	result.ReasoningTokens = nil
	result.NonReasoningTokens = nil
	result.DraftTokens = 0
	result.DraftAccepted = 0
	result.DraftAcceptance = nil
	result.Measurements = windows
	result.Runs = count
	result.MeasuredAt = time.Now()
	return result, nil
}

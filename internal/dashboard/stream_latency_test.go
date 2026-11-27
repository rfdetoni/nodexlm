package dashboard

import (
	"testing"
	"time"
)

func TestOutputLatencyIgnoresRoleAndUsage(t *testing.T) {
	start := time.Now()
	l := streamLatency{start: start}
	l.observe([]byte(`{"choices":[{"delta":{"role":"assistant"}}]}`), start.Add(time.Millisecond))
	l.observe([]byte(`{"choices":[{"delta":{"content":"hello"}}]}`), start.Add(25*time.Millisecond))
	l.observe([]byte(`{"usage":{"completion_tokens":4}}`), start.Add(30*time.Millisecond))
	l.observe([]byte(`{"choices":[{"delta":{"content":" world"}}]}`), start.Add(35*time.Millisecond))
	var s InferenceSample
	l.apply(&s)
	if s.FirstOutputMilliseconds != 25 || s.OutputChunkP50Milliseconds != 10 {
		t.Fatalf("%+v", s)
	}
}

func TestReasoningLatencyPrecedesVisibleText(t *testing.T) {
	start := time.Now()
	l := streamLatency{start: start}
	l.observe([]byte(`{"choices":[{"delta":{"reasoning_content":"think"}}]}`), start.Add(25*time.Millisecond))
	l.observe([]byte(`{"choices":[{"delta":{"content":"answer"}}]}`), start.Add(125*time.Millisecond))
	var sample InferenceSample
	l.apply(&sample)
	if sample.FirstOutputMilliseconds != 25 || sample.FirstTextMilliseconds == nil ||
		*sample.FirstTextMilliseconds != 125 || sample.FirstReasoningMilliseconds == nil ||
		*sample.FirstReasoningMilliseconds != 25 {
		t.Fatalf("wrong output latency: %+v", sample)
	}
}

func TestObservedReasoningAndAnswerPhaseRatesRequireReportedTokenCounts(t *testing.T) {
	start := time.Now()
	latency := streamLatency{start: start}
	latency.observe([]byte(`{"choices":[{"delta":{"reasoning_content":"thinking"}}]}`), start.Add(100*time.Millisecond))
	latency.observe([]byte(`{"choices":[{"delta":{"reasoning_content":"more"}}]}`), start.Add(200*time.Millisecond))
	latency.observe([]byte(`{"choices":[{"delta":{"content":"hello"}}]}`), start.Add(300*time.Millisecond))
	latency.observe([]byte(`{"choices":[{"delta":{"content":" world"}}]}`), start.Add(500*time.Millisecond))
	// Counts are supplied by the runtime, never estimated from SSE strings.
	reasoning, answer := 20, 10
	sample := InferenceSample{ReasoningTokens: &reasoning, NonReasoningTokens: &answer}
	latency.apply(&sample)
	if sample.PhaseRateBasis != "sse_event_window" ||
		sample.ObservedReasoningTokensPerSecond == nil ||
		sample.ObservedAnswerTokensPerSecond == nil ||
		*sample.ObservedReasoningTokensPerSecond != 100 ||
		*sample.ObservedAnswerTokensPerSecond != 50 {
		t.Fatalf("missing observed phase throughput: %+v", sample)
	}
	unknown := InferenceSample{}
	latency.apply(&unknown)
	if unknown.PhaseRateBasis != "" || unknown.ObservedReasoningTokensPerSecond != nil {
		t.Fatalf("stream event text cannot be used as a token count: %+v", unknown)
	}
}

func TestObservedPhaseRatesRejectInterleavingAndSingleChunkAnswer(t *testing.T) {
	start := time.Now()
	reasoning, answer := 20, 10
	for _, mixed := range [][]string{
		{`{"choices":[{"delta":{"reasoning_content":"think"}}]}`,
			`{"choices":[{"delta":{"content":"answer","reasoning_content":"again"}}]}`,
			`{"choices":[{"delta":{"content":"finish"}}]}`},
		{`{"choices":[{"delta":{"content":"answer"}}]}`,
			`{"choices":[{"delta":{"reasoning_content":"think"}}]}`,
			`{"choices":[{"delta":{"content":"finish"}}]}`},
	} {
		latency := streamLatency{start: start}
		for i, frame := range mixed {
			latency.observe([]byte(frame), start.Add(time.Duration(1+i)*100*time.Millisecond))
		}
		sample := InferenceSample{ReasoningTokens: &reasoning, NonReasoningTokens: &answer}
		latency.apply(&sample)
		if sample.PhaseRateBasis != "" || sample.ObservedReasoningTokensPerSecond != nil ||
			sample.ObservedAnswerTokensPerSecond != nil {
			t.Fatalf("interleaved SSE is not a measurable phase boundary: %+v", sample)
		}
	}
	latency := streamLatency{start: start}
	latency.observe([]byte(`{"choices":[{"delta":{"reasoning_content":"think"}}]}`), start.Add(100*time.Millisecond))
	latency.observe([]byte(`{"choices":[{"delta":{"content":"one event"}}]}`), start.Add(200*time.Millisecond))
	sample := InferenceSample{ReasoningTokens: &reasoning, NonReasoningTokens: &answer}
	latency.apply(&sample)
	if sample.ObservedReasoningTokensPerSecond == nil || sample.ObservedAnswerTokensPerSecond != nil {
		t.Fatalf("single text event has no measurable visible interval: %+v", sample)
	}
}

func TestObservedPhaseRatesRejectMultipleChoices(t *testing.T) {
	start := time.Now()
	for _, frames := range [][]string{
		{`{"choices":[{"index":0,"delta":{"reasoning_content":"think"}},{"index":1,"delta":{"content":"other"}}]}`, `{"choices":[{"index":0,"delta":{"content":"answer"}}]}`},
		{`{"choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`, `{"choices":[{"index":1,"delta":{"content":"answer"}}]}`},
	} {
		latency := streamLatency{start: start}
		for i, frame := range frames {
			latency.observe([]byte(frame), start.Add(time.Duration(i+1)*time.Millisecond))
		}
		count := 5
		sample := InferenceSample{ReasoningTokens: &count, NonReasoningTokens: &count}
		latency.apply(&sample)
		if sample.PhaseRateBasis != "" || sample.ObservedReasoningTokensPerSecond != nil || sample.ObservedAnswerTokensPerSecond != nil {
			t.Fatalf("multi-choice usage cannot be attributed: %+v", sample)
		}
	}
}

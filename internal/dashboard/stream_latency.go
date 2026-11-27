package dashboard

import (
	"encoding/json"
	"sort"
	"time"
)

// SSE events can contain multiple tokens: these are output-chunk metrics,
// not fabricated exact per-token timing.
type streamLatency struct {
	start, first, last        time.Time
	firstText, firstReasoning time.Time
	lastText                  time.Time
	interleaved               bool
	choiceIndex               int
	choiceSeen                bool
	gaps                      []float64
}

func (l *streamLatency) observe(payload []byte, now time.Time) {
	var event struct {
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Content   string            `json:"content"`
				Reasoning string            `json:"reasoning_content"`
				Tools     []json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(payload, &event) != nil {
		return
	}
	if len(event.Choices) > 1 {
		l.interleaved = true
	}
	observed := false
	for _, choice := range event.Choices {
		d := choice.Delta
		if d.Content == "" && d.Reasoning == "" && len(d.Tools) == 0 {
			continue
		}
		if !l.choiceSeen {
			l.choiceIndex, l.choiceSeen = choice.Index, true
		} else if l.choiceIndex != choice.Index {
			l.interleaved = true
		}
		// Do not infer separate phase throughput when a stream mixes
		// visible text and reasoning in the same event, or resumes reasoning
		// after it has already begun emitting visible text.
		if d.Reasoning != "" {
			if !l.firstText.IsZero() || d.Content != "" {
				l.interleaved = true
			}
			if l.firstReasoning.IsZero() {
				l.firstReasoning = now
			}
		}
		if d.Content != "" {
			if l.firstText.IsZero() {
				l.firstText = now
			}
			l.lastText = now
		}
		if len(d.Tools) != 0 {
			l.interleaved = true
		}
		observed = true
	}
	if observed {
		if l.first.IsZero() {
			l.first = now
		}
		if !l.last.IsZero() && len(l.gaps) < 4096 {
			l.gaps = append(l.gaps, float64(now.Sub(l.last))/float64(time.Millisecond))
		}
		l.last = now
	}
}

func (l streamLatency) apply(s *InferenceSample) {
	if !l.first.IsZero() && !l.start.IsZero() {
		s.FirstOutputMilliseconds = float64(l.first.Sub(l.start)) / float64(time.Millisecond)
	}
	if !l.start.IsZero() {
		if !l.firstText.IsZero() {
			ms := float64(l.firstText.Sub(l.start)) / float64(time.Millisecond)
			s.FirstTextMilliseconds = &ms
		}
		if !l.firstReasoning.IsZero() {
			ms := float64(l.firstReasoning.Sub(l.start)) / float64(time.Millisecond)
			s.FirstReasoningMilliseconds = &ms
		}
	}
	// Usage reports actual token counts; SSE gives event-level phase
	// boundaries, NOT exact per-token timestamps. Report phase-window
	// rates only for strictly ordered streams with both reported counts.
	if !l.interleaved && s.ReasoningTokens != nil && s.NonReasoningTokens != nil &&
		*s.ReasoningTokens > 0 && *s.NonReasoningTokens > 0 &&
		!l.firstReasoning.IsZero() && !l.firstText.IsZero() &&
		l.firstText.After(l.firstReasoning) {
		reasonSeconds := l.firstText.Sub(l.firstReasoning).Seconds()
		rate := float64(*s.ReasoningTokens) / reasonSeconds
		s.ObservedReasoningTokensPerSecond = &rate
		s.PhaseRateBasis = "sse_event_window"
		if l.lastText.After(l.firstText) {
			visibleSeconds := l.lastText.Sub(l.firstText).Seconds()
			visibleRate := float64(*s.NonReasoningTokens) / visibleSeconds
			s.ObservedAnswerTokensPerSecond = &visibleRate
		}
	}
	if len(l.gaps) > 0 {
		sort.Float64s(l.gaps)
		s.OutputChunkP50Milliseconds = l.gaps[(len(l.gaps)-1)*50/100]
		s.OutputChunkP95Milliseconds = l.gaps[(len(l.gaps)-1)*95/100]
	}
}

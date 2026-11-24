package dashboard

import (
	"encoding/json"
	"sort"
	"time"
)

// SSE events can contain multiple tokens: these are output-chunk metrics,
// not fabricated exact per-token timing.
type streamLatency struct {
	start, first, last time.Time
	gaps               []float64
}

func (l *streamLatency) observe(payload []byte, now time.Time) {
	var event struct {
		Choices []struct {
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
	for _, choice := range event.Choices {
		d := choice.Delta
		if d.Content == "" && d.Reasoning == "" && len(d.Tools) == 0 {
			continue
		}
		if l.first.IsZero() {
			l.first = now
		}
		if !l.last.IsZero() && len(l.gaps) < 4096 {
			l.gaps = append(l.gaps, float64(now.Sub(l.last))/float64(time.Millisecond))
		}
		l.last = now
		break
	}
}

func (l streamLatency) apply(s *InferenceSample) {
	if !l.first.IsZero() && !l.start.IsZero() {
		s.FirstOutputMilliseconds = float64(l.first.Sub(l.start))/float64(time.Millisecond)
	}
	if len(l.gaps) > 0 {
		sort.Float64s(l.gaps)
		s.OutputChunkP50Milliseconds = l.gaps[(len(l.gaps)-1)*50/100]
		s.OutputChunkP95Milliseconds = l.gaps[(len(l.gaps)-1)*95/100]
	}
}

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

package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

type benchmarkProgressKey struct{}
type benchmarkRoundKey struct{}

type benchmarkProgressEvent struct {
	Type       string                     `json:"type"`
	Phase      string                     `json:"phase,omitempty"`
	Detail     string                     `json:"detail,omitempty"`
	Round      int                        `json:"round,omitempty"`
	Rounds     int                        `json:"rounds,omitempty"`
	Current    int                        `json:"current,omitempty"`
	Total      int                        `json:"total,omitempty"`
	ElapsedMS  int64                      `json:"elapsed_ms,omitempty"`
	PhaseMS    int64                      `json:"phase_ms,omitempty"`
	Tokens     int                        `json:"tokens,omitempty"`
	Rate       float64                    `json:"rate,omitempty"`
	RateBasis  string                     `json:"rate_basis,omitempty"`
	Prefill    float64                    `json:"prefill,omitempty"`
	DurationMS int64                      `json:"duration_ms,omitempty"`
	Traffic    []benchmarkProgressTraffic `json:"traffic,omitempty"`
	Result     any                        `json:"result,omitempty"`
	Error      string                     `json:"error,omitempty"`

	// Internal fields never enter JSON.
	rpcGuests []RPCGuest
	baseline  map[string]rpcmesh.TunnelStats
}

type benchmarkProgressTraffic struct {
	Name     string `json:"name"`
	Sent     uint64 `json:"sent_bytes"`
	Received uint64 `json:"received_bytes"`
}

func benchmarkProgress(ctx context.Context, event benchmarkProgressEvent) {
	report, ok := ctx.Value(benchmarkProgressKey{}).(func(benchmarkProgressEvent))
	if !ok {
		return
	}
	event.Type = "progress"
	if round, ok := ctx.Value(benchmarkRoundKey{}).([2]int); ok {
		event.Round, event.Rounds = round[0], round[1]
	}
	report(event)
}

func benchmarkPhase(ctx context.Context, phase, detail string) {
	benchmarkProgress(ctx, benchmarkProgressEvent{Phase: phase, Detail: detail})
}

func benchmarkMeasured(ctx context.Context, phase, detail string, current, total int, sample InferenceSample) {
	benchmarkProgress(ctx, benchmarkProgressEvent{Phase: phase, Detail: detail, Current: current, Total: total,
		Tokens: sample.Tokens, Rate: sample.TokensPerSecond, RateBasis: sample.RateBasis, Prefill: sample.PrefillTokensPerSecond})
}

func (m *Manager) benchmarkLoadProgress(ctx context.Context, guests []RPCGuest, size int64) {
	if ctx.Value(benchmarkProgressKey{}) == nil {
		return
	}
	event := benchmarkProgressEvent{Phase: "load", Detail: fmt.Sprintf("GGUF: %.2f GiB; %d Guest(s)", float64(size)/(1<<30), len(guests)), rpcGuests: guests}
	m.mu.Lock()
	stats := m.rpcStatsProvider
	m.mu.Unlock()
	if stats != nil && len(guests) > 0 {
		event.baseline = stats()
	}
	benchmarkProgress(ctx, event)
}

func (m *Manager) benchmarkTraffic(event benchmarkProgressEvent) []benchmarkProgressTraffic {
	if len(event.baseline) == 0 || len(event.rpcGuests) == 0 {
		return nil
	}
	m.mu.Lock()
	stats := m.rpcStatsProvider
	m.mu.Unlock()
	if stats == nil {
		return nil
	}
	current := stats()
	var rows []benchmarkProgressTraffic
	for i, guest := range event.rpcGuests {
		a, aOK := event.baseline[guest.ID]
		b, bOK := current[guest.ID]
		if !aOK || !bOK || a.Epoch == 0 || a.Epoch != b.Epoch || b.BytesToGuest < a.BytesToGuest || b.BytesFromGuest < a.BytesFromGuest {
			continue
		}
		name := guest.Name
		if name == "" {
			name = fmt.Sprintf("Guest %d", i+1)
		}
		rows = append(rows, benchmarkProgressTraffic{Name: name, Sent: b.BytesToGuest - a.BytesToGuest, Received: b.BytesFromGuest - a.BytesFromGuest})
	}
	return rows
}

type benchmarkStreamInput struct {
	Action   string `json:"action"`
	Model    string `json:"model"`
	Node     string `json:"node_id,omitempty"`
	Extended bool   `json:"extended,omitempty"`
	Rounds   int    `json:"rounds,omitempty"`
	Minutes  int    `json:"minutes,omitempty"`
}

func (d *Dashboard) benchmarkStream(w http.ResponseWriter, r *http.Request) {
	var input benchmarkStreamInput
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if !decode(w, r, &input) {
		return
	}
	switch input.Action {
	case "single", "identity":
	case "series":
		if input.Rounds < 2 || input.Rounds > 10 {
			failure(w, 400, errors.New("cluster series requires 2–10 rounds"))
			return
		}
	case "stability":
		if input.Minutes < 1 || input.Minutes > 60 {
			failure(w, 400, errors.New("stability duration must be 1–60 minutes"))
			return
		}
	default:
		failure(w, 400, errors.New("unknown benchmark action"))
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		failure(w, 500, errors.New("streaming unavailable"))
		return
	}
	if !d.benchmarkStreaming.CompareAndSwap(false, true) {
		failure(w, 409, errors.New("another benchmark stream is active"))
		return
	}
	// Loading distributed weights may take hours on slow Guests. Only the
	// request lifetime (including explicit cancellation) bounds the benchmark.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events := make(chan benchmarkProgressEvent, 16)
	send := func(event benchmarkProgressEvent) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	}
	ctx = context.WithValue(ctx, benchmarkProgressKey{}, send)
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	controller := http.NewResponseController(w)
	write := func(event benchmarkProgressEvent) bool {
		// Bound slow-client writes; ResponseRecorder does not support deadlines.
		_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return encoder.Encode(event) == nil && controller.Flush() == nil
	}
	go func() {
		defer close(events)
		var result any
		var err error
		switch input.Action {
		case "single":
			result, err = d.Manager.benchmarkInference(ctx, input.Model, input.Node, input.Extended)
		case "series":
			result, err = d.Manager.benchmarkSeries(ctx, input.Model, "cluster", false, input.Rounds)
		case "stability":
			var rows []benchmarkRound
			rows, err = d.Manager.benchmarkSeriesDuration(ctx, input.Model, input.Node, false, 1, time.Duration(input.Minutes)*time.Minute)
			if err == nil {
				result = rows[0].Inference
			}
		case "identity":
			result, err = d.Manager.benchmarkModelIdentity(ctx, input.Model)
		}
		// Release execution ownership before the terminal event reaches the client.
		d.benchmarkStreaming.Store(false)
		if err != nil {
			send(benchmarkProgressEvent{Type: "error", Error: err.Error()})
		} else {
			send(benchmarkProgressEvent{Type: "result", Result: result})
		}
	}()
	started := time.Now()
	phaseStarted := started
	phase := benchmarkProgressEvent{Type: "progress", Phase: "prepare", Detail: ""}
	if !write(phase) {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if r.Context().Err() == nil {
				write(benchmarkProgressEvent{Type: "error", Error: ctx.Err().Error()})
			}
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			event.ElapsedMS = time.Since(started).Milliseconds()
			if event.Type == "progress" {
				if phase.Phase == "load" && event.Phase != "load" {
					if traffic := d.Manager.benchmarkTraffic(phase); len(traffic) > 0 {
						if !write(benchmarkProgressEvent{Type: "heartbeat", Phase: "load", Traffic: traffic, PhaseMS: time.Since(phaseStarted).Milliseconds()}) {
							return
						}
					}
				}
				if event.Phase != phase.Phase || event.Round != phase.Round || event.Detail != phase.Detail {
					phaseStarted = time.Now()
				}
				phase = event
				event.PhaseMS = time.Since(phaseStarted).Milliseconds()
			}
			if !write(event) || event.Type == "result" || event.Type == "error" {
				return
			}
		case <-ticker.C:
			event := benchmarkProgressEvent{Type: "heartbeat", Phase: phase.Phase, Round: phase.Round, Rounds: phase.Rounds,
				ElapsedMS: time.Since(started).Milliseconds(), PhaseMS: time.Since(phaseStarted).Milliseconds(), DurationMS: phase.DurationMS}
			if phase.Phase == "load" {
				event.Traffic = d.Manager.benchmarkTraffic(phase)
			}
			if !write(event) {
				return
			}
		}
	}
}

package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// Use one bound for dashboard and OpenAI-compatible inference.
func (m *Manager) inferenceTimeout(model string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A saved distribution preference is not the topology of the loaded
	// process; speed placement may have selected Host-only instead.
	if m.engine != nil && (model == "" || model == m.current) &&
		len(m.engine.RPCServers) > 0 {
		return 20 * time.Minute
	}
	return 4 * time.Minute
}

// Admission is cancellable and tied to the loaded engine generation.
func (m *Manager) withGeneration(ctx context.Context, req protocol.ChatRequest, generate func(context.Context, *local.Managed, protocol.ChatRequest) error) error {
	m.mu.Lock()
	child := m.residents[req.Model]
	engine, admission := m.engine, m.admission
	m.mu.Unlock()
	if child != nil {
		start := time.Now()
		err := child.withGeneration(ctx, req, generate)
		m.recordGeneration(time.Since(start), err)
		return err
	}
	if engine == nil {
		return errors.New("no model loaded")
	}
	select {
	case admission <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-admission }()
	m.chatMu.RLock()
	defer m.chatMu.RUnlock()
	m.mu.Lock()
	if m.engine != engine || m.unloading || m.closed || req.Model != "" && req.Model != m.current {
		m.mu.Unlock()
		return errors.New("model changed or unloaded")
	}
	generationCtx, cancel := context.WithCancel(ctx)
	m.generationSequence++
	id := m.generationSequence
	if m.generations == nil {
		m.generations = make(map[uint64]context.CancelFunc)
	}
	m.generations[id] = cancel
	req.Model = m.current
	provider := m.rpcStatsProvider
	nodes := append([]string(nil), m.loadRPCNodes...)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.generations, id)
		m.mu.Unlock()
		cancel()
	}()
	start := time.Now()
	if len(engine.RPCServers) > 0 && provider != nil && len(nodes) > 0 {
		baseline := provider()
		err := generate(generationCtx, engine, req)
		if err == nil {
			m.recordRPCTraffic(req.Model, start, nodes, baseline, provider())
		}
		m.recordGeneration(time.Since(start), err)
		return err
	}
	err := generate(generationCtx, engine, req)
	m.recordGeneration(time.Since(start), err)
	return err
}
func (m *Manager) GenerateStream(ctx context.Context, req protocol.ChatRequest, emit func([]byte) error) error {
	return m.withGeneration(ctx, req, func(ctx context.Context, engine *local.Managed, req protocol.ChatRequest) error {
		start := time.Now()
		var usage []byte
		latency := streamLatency{start: start}
		err := engine.GenerateStream(ctx, req, func(frame []byte) error {
			payload := bytes.TrimSpace(bytes.TrimPrefix(frame, []byte("data: ")))
			latency.observe(payload, time.Now())
			if bytes.Contains(payload, []byte(`"usage"`)) {
				usage = append([]byte(nil), payload...)
			}
			return emit(frame)
		})
		if err == nil {
			m.recordInferenceWithLatency(req.Model, usage, time.Since(start), latency)
		}
		return err
	})
}
func (m *Manager) Complete(ctx context.Context, req protocol.ChatRequest) (json.RawMessage, error) {
	var raw json.RawMessage
	err := m.withGeneration(ctx, req, func(ctx context.Context, engine *local.Managed, req protocol.ChatRequest) error {
		var err error
		start := time.Now()
		raw, err = engine.Complete(ctx, req)
		if err == nil {
			m.recordInference(req.Model, raw, time.Since(start))
		}
		return err
	})
	return raw, err
}

func (d *Dashboard) chat(w http.ResponseWriter, r *http.Request) {
	var req protocol.ChatRequest
	if !decode(w, r, &req) {
		return
	}
	current, _ := d.Manager.State()["model"].(string)
	if req.Model == "" {
		req.Model = current
	}
	if why := protocol.ValidateChat(req); why != "" {
		failure(w, 400, errors.New(why))
		return
	}
	if req.TargetNode != "" {
		d.Cluster.QueueChat(w, r, req)
		return
	}
	if !d.Manager.HasModel(req.Model) && current == "" {
		failure(w, 409, errors.New("load a model on the Host first"))
		return
	}
	if !d.Manager.HasModel(req.Model) {
		failure(w, 404, errors.New("requested model is not loaded"))
		return
	}
	// Distributed prefill/first-token latency can exceed the legacy
	// 3-minute backend Client.Timeout. Bound the whole request at the
	// API boundary, retaining prompt cancellation and a finite deadline.
	limit := d.Manager.inferenceTimeout(req.Model)
	ctx, cancel := context.WithTimeout(r.Context(), limit)
	defer cancel()
	if req.Stream {
		started := false
		err := d.Manager.GenerateStream(ctx, req, func(frame []byte) error {
			if !started {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Accel-Buffering", "no")
				started = true
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := w.Write(frame); err != nil {
				return err
			}
			return http.NewResponseController(w).Flush()
		})
		if err != nil && ctx.Err() == nil {
			if !started {
				failure(w, 502, err)
			} else { // SSE errors are framed after headers have been sent.
				writeStreamError(w, err)
			}
		}
		return
	}

	raw, err := d.Manager.Complete(ctx, req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			failure(w, http.StatusGatewayTimeout, fmt.Errorf("inference exceeded %s; check RPC Guest availability and /api/status rpc_transfer before retrying: %w", limit, err))
		} else {
			failure(w, 502, err)
		}
		return
	}
	if r.URL.Path == "/api/chat" {
		var out struct {
			Choices []struct {
				Message protocol.Message `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
			failure(w, 502, errors.New("invalid completion"))
			return
		}
		reply(w, 200, map[string]string{"answer": out.Choices[0].Message.Content})
		return
	}
	// Preserve the backend's completion ID, usage and finish_reason.
	reply(w, 200, raw)

}

func writeStreamError(w http.ResponseWriter, err error) {
	raw, _ := json.Marshal(map[string]any{"error": map[string]string{"message": err.Error(), "type": "inference_error"}})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	_ = http.NewResponseController(w).Flush()
}

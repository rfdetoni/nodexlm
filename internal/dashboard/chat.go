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

// Serialize only the native generation. Admission is cancellable,
// so a disconnected client cannot hold the next request in a mutex queue.
func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.admission <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) withGeneration(ctx context.Context, req protocol.ChatRequest, generate func(context.Context, *local.Managed, protocol.ChatRequest) error) error {
	m.mu.Lock()
	child := m.residents[req.Model]
	m.mu.Unlock()
	if child != nil {
		start := time.Now()
		err := child.withGeneration(ctx, req, generate)
		m.recordGeneration(time.Since(start), err)
		return err
	}
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-m.admission }()
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	m.mu.Lock()
	engine := m.engine
	if engine == nil || m.unloading || req.Model != "" && req.Model != m.current {
		m.mu.Unlock()
		return errors.New("no model loaded")
	}
	generationCtx, cancel := context.WithCancel(ctx)
	m.generationCancel = cancel
	req.Model = m.current
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.generationCancel = nil; m.mu.Unlock(); cancel() }()
	start := time.Now()
	err := generate(generationCtx, engine, req)
	m.recordGeneration(time.Since(start), err)
	return err
}
func (m *Manager) GenerateStream(ctx context.Context, req protocol.ChatRequest, emit func([]byte) error) error {
	return m.withGeneration(ctx, req, func(ctx context.Context, engine *local.Managed, req protocol.ChatRequest) error {
		start := time.Now()
		var usage []byte
		err := engine.GenerateStream(ctx, req, func(frame []byte) error {
			payload := bytes.TrimSpace(bytes.TrimPrefix(frame, []byte("data: ")))
			if bytes.Contains(payload, []byte(`"usage"`)) {
				usage = append([]byte(nil), payload...)
			}
			return emit(frame)
		})
		if err == nil {
			m.recordInference(req.Model, usage, time.Since(start))
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
	limit := 4 * time.Minute
	if d.Manager.Settings().Distributed {
		limit = 20 * time.Minute
	}
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

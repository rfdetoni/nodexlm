package local

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// GenerateStream forwards complete OpenAI SSE events, preserving usage and
// finish reasons. A broken backend stream is an error, never a false [DONE].
func (e *Engine) GenerateStream(ctx context.Context, request protocol.ChatRequest, emit func([]byte) error) error {
	request.Stream = true
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BackendURL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	res, err := e.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 200))
		return fmt.Errorf("llama-server HTTP %d: %s", res.StatusCode, body)
	}
	if !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		return errors.New("backend did not return an SSE stream")
	}
	scan := bufio.NewScanner(res.Body)
	scan.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	send := func() (bool, error) {
		if len(data) == 0 {
			return false, nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		if payload != "[DONE]" && !json.Valid([]byte(payload)) {
			return false, errors.New("invalid backend SSE JSON")
		}
		if err := emit([]byte("data: " + payload + "\n\n")); err != nil {
			return false, err
		}
		return payload == "[DONE]", nil
	}
	size := 0
	for scan.Scan() {
		line := scan.Text()
		size += len(line)
		if size > 1<<20 {
			return errors.New("backend SSE event too large")
		}
		if line == "" {
			done, err := send()
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			size = 0
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	return errors.New("backend stream ended before [DONE]")
}

func (m *Managed) withEndpoint(ctx context.Context, generate func(*Engine) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	if err := m.ensure(ctx); err != nil {
		return err
	}
	defer func() {
		if !m.KeepLoaded {
			if m.Idle <= 0 {
				m.stopLocked()
			} else {
				m.idleTimer = time.AfterFunc(m.Idle, func() { m.mu.Lock(); defer m.mu.Unlock(); m.stopLocked() })
			}
		}
	}()
	return generate(m.endpoint)
}
func (m *Managed) GenerateStream(ctx context.Context, req protocol.ChatRequest, emit func([]byte) error) error {
	return m.withEndpoint(ctx, func(engine *Engine) error { return engine.GenerateStream(ctx, req, emit) })
}
func (m *Managed) Complete(ctx context.Context, req protocol.ChatRequest) (json.RawMessage, error) {
	var raw json.RawMessage
	err := m.withEndpoint(ctx, func(engine *Engine) error { var err error; raw, err = engine.Complete(ctx, req); return err })
	return raw, err
}

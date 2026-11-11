package local

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// OpenAI forwards only an explicit inference route, never a client URL or token.
func (e *Engine) OpenAI(ctx context.Context, path string, body []byte, receive func(int, http.Header, io.Reader) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BackendURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	response, err := e.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return receive(response.StatusCode, response.Header, response.Body)
}
func (m *Managed) OpenAI(ctx context.Context, path string, body []byte, receive func(int, http.Header, io.Reader) error) error {
	normalized, err := m.normalizeOpenAI(body)
	if err != nil {return err}
	return m.withEndpoint(ctx, func(e *Engine) error { return e.OpenAI(ctx, path, normalized, receive) })
}

package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NewLiteRT connects to the official, separately installed LiteRT-LM server.
// Its registry owns model loading and hardware selection. Never expose its
// unauthenticated HTTP listener beyond loopback or forward a remote redirect.
func NewLiteRT(ctx context.Context, endpoint, model string) (*Engine, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("LiteRT-LM requires a loopback HTTP endpoint without credentials or path")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("LiteRT-LM requires a literal loopback IP")
	}
	if model == "" || len(model) > 512 || strings.ContainsAny(model, "\r\n\x00") {
		return nil, errors.New("invalid LiteRT-LM registry model ID")
	}
	engine, err := New(endpoint)
	if err != nil {
		return nil, err
	}
	engine.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, engine.BackendURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := engine.HTTP.Do(req)
	if err != nil {
		engine.HTTP.CloseIdleConnections()
		return nil, fmt.Errorf("LiteRT-LM server unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LiteRT-LM model registry returned HTTP %d", resp.StatusCode)
	}
	var registry struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&registry); err != nil {
		return nil, fmt.Errorf("invalid LiteRT-LM model registry: %w", err)
	}
	for _, entry := range registry.Data {
		if entry.ID == model {
			return engine, nil
		}
	}
	return nil, errors.New("model not found in LiteRT-LM registry")
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DecisionEngine computes a typed answer, not generated text.
type DecisionEngine interface {
	Decide(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

// LocalDecisionEngine forwards to an operator-configured, loopback-only
// System One runtime (Ollaya or native llama-server).
type LocalDecisionEngine struct {
	origin string
	apiKey string
	http   *http.Client
}

// NewLocalDecisionEngine never contacts hosts controlled by the incoming job.
// Remote Host requests arrive only on the pinned coordinator connection.
func NewLocalDecisionEngine(raw string) (*LocalDecisionEngine, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" ||
		net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, errors.New("decision backend must be a numeric HTTP loopback origin")
	}
	return &LocalDecisionEngine{origin: strings.TrimSuffix(raw, "/"), apiKey: os.Getenv("NODEXLM_DECISION_API_KEY"), http: &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
			MaxIdleConnsPerHost: 2,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("upstream redirects not allowed") },
	}}, nil
}

// Check returns only if the model is actually listed by the local runtime.
// It avoids advertising a decision worker with no usable model loaded.
func (e *LocalDecisionEngine) Check(ctx context.Context, model string) error {
	model = strings.TrimPrefix(model, "ollaya/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.origin+"/v1/models", nil)
	if err != nil {
		return err
	}
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	response, err := e.http.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("decision runtime models HTTP %d", response.StatusCode)
	}
	var models struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&models) != nil {
		return errors.New("malformed decision runtime model inventory")
	}
	for _, m := range models.Models {
		if m.Name == model {
			return nil
		}
	}
	for _, m := range models.Data {
		if m.ID == model {
			return nil
		}
	}
	return fmt.Errorf("decision model %q not advertised by runtime", model)
}

func (e *LocalDecisionEngine) Decide(ctx context.Context, model string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > 512<<10 {
		return nil, errors.New("invalid decision size")
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil || input == nil {
		return nil, errors.New("invalid decision object")
	}
	input["model"], _ = json.Marshal(strings.TrimPrefix(model, "ollaya/"))
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.origin+"/v1/systemone", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	response, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, (512<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(contents) > 512<<10 || !json.Valid(contents) {
		return nil, errors.New("invalid or excessive decision runtime output")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("decision runtime HTTP %d", response.StatusCode)
	}
	return contents, nil
}

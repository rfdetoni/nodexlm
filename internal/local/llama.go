// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

type Engine struct {
	BackendURL string
	HTTP       *http.Client
	APIKey     string // Ephemeral private llama-server key, never exposed to clients.
}

func New(backend string) (*Engine, error) {
	u, err := url.Parse(backend)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, errors.New("backend must be a loopback HTTP llama-server endpoint")
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil, errors.New("backend must use loopback; external runtimes are not trusted")
	}
	return &Engine{BackendURL: strings.TrimRight(backend, "/"), HTTP: &http.Client{Timeout: 3 * time.Minute, Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, MaxIdleConnsPerHost: 1}}}, nil
}

func (e *Engine) Generate(ctx context.Context, request protocol.ChatRequest) (string, error) {
	request.Stream = false
	data, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	r, err := http.NewRequestWithContext(ctx, "POST", e.BackendURL+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	r.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		r.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	resp, err := e.HTTP.Do(r)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("llama-server HTTP %d: %.200s", resp.StatusCode, raw)
	}
	var out struct {
		Choices []struct {
			Message protocol.Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", errors.New("backend returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}

// Scan GGUF files with a bounded read. Never load model weights into Go RAM.
func Models(root string) ([]protocol.ModelRecord, error) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	items := make([]protocol.ModelRecord, 0)
	for _, d := range dirs {
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".gguf") {
			continue
		}
		p := filepath.Join(root, d.Name())
		meta, err := InspectGGUF(p)
		if err != nil {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		items = append(items, protocol.ModelRecord{Name: d.Name(), Size: info.Size(), Architecture: meta.Architecture, MoE: meta.IsMoE, ExpertCount: meta.ExpertCount, ActiveExperts: meta.ActiveExperts})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

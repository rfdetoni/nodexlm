// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
)

// Search is explicitly requested by an administrator, not a resident scanner.
// Repository/file names are treated as data and never executed.
type Hub struct {
	Client  *http.Client
	APIBase string
}

func (h Hub) get(ctx context.Context, path string, output any) error {
	base := h.APIBase
	if base == "" {
		base = "https://huggingface.co"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return err
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("Hugging Face API HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return err
	}
	if len(body) > 4<<20 {
		return errors.New("catalog response too large")
	}
	return json.Unmarshal(body, output)
}
func (h Hub) Search(ctx context.Context, query string) ([]clusterplan.ModelCandidate, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 || len(query) > 64 {
		return nil, errors.New("search must contain 2–64 characters")
	}
	var models []struct {
		ID    string `json:"id"`
		Gated any    `json:"gated"`
	}
	q := url.Values{"search": {query}, "filter": {"gguf"}, "sort": {"downloads"}, "direction": {"-1"}, "limit": {"8"}}
	if err := h.get(ctx, "/api/models?"+q.Encode(), &models); err != nil {
		return nil, err
	}
	var result []clusterplan.ModelCandidate
	for _, model := range models {
		if len(result) >= 20 {
			break
		}
		parts := strings.Split(model.ID, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		if model.Gated == true || model.Gated == "auto" || model.Gated == "manual" {
			continue
		}
		// Up to eight repositories, and only plain, single-file GGUF quantizations.
		var files []struct {
			Type string `json:"type"`
			Path string `json:"path"`
			Size int64  `json:"size"`
		}
		path := "/api/models/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/tree/main?recursive=true&limit=1000"
		if err := h.get(ctx, path, &files); err != nil {
			continue
		}
		count := 0
		for _, f := range files {
			if count == 3 || len(result) >= 20 {
				break
			}
			lower := strings.ToLower(f.Path)
			if f.Type != "file" || !strings.HasSuffix(lower, ".gguf") || f.Size <= 0 || strings.Contains(lower, "-of-") {
				continue
			}
			if strings.Contains(f.Path, "..") || strings.ContainsAny(f.Path, "\\\x00") {
				continue
			}
			location := "https://huggingface.co/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/resolve/main/" + strings.ReplaceAll(url.PathEscape(f.Path), "%2F", "/")
			result = append(result, clusterplan.ModelCandidate{Repository: model.ID, Filename: f.Path, SizeBytes: uint64(f.Size), URL: location})
			count++
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SizeBytes < result[j].SizeBytes })
	return result, nil
}

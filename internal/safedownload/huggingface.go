package safedownload

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PlanHuggingFaceBatch pins one immutable commit and exact content lengths.
// Unsupported servers can retain the sequential path; a discovered revision
// must not be silently replaced by a mutable branch after a metadata failure.
func PlanHuggingFaceBatch(ctx context.Context, client *http.Client, files []BatchFile) ([]BatchFile, bool, error) {
	if len(files) < 2 || len(files) > 128 {
		return nil, false, nil
	}
	parsed := make([]*url.URL, len(files))
	var repository string
	for i, file := range files {
		u, err := url.Parse(file.Source)
		if err != nil || ValidateURL(u) != nil || u.Host != "huggingface.co" || u.RawQuery != "" || u.ForceQuery {
			return nil, false, nil
		}
		parts := strings.SplitN(strings.TrimPrefix(u.EscapedPath(), "/"), "/", 5)
		if len(parts) != 5 || parts[2] != "resolve" || parts[3] != "main" {
			return nil, false, nil
		}
		key := parts[0] + "/" + parts[1]
		if i == 0 {
			repository = key
		} else if repository != key {
			return nil, false, nil
		}
		parsed[i] = u
	}
	if client == nil {
		client = Client()
	}
	head := func(remote string) (string, int64, error) {
		probe, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(probe, http.MethodHead, remote, nil)
		if err != nil {
			return "", 0, err
		}
		req.Header.Set("Accept-Encoding", "identity")
		resp, err := client.Do(req)
		if err != nil {
			return "", 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", 0, fmt.Errorf("model metadata HTTP %d", resp.StatusCode)
		}
		var revision string
		for current := resp; current != nil; {
			if v := current.Header.Get("X-Repo-Commit"); v != "" {
				revision = v
			}
			if current.Request == nil {
				break
			}
			current = current.Request.Response
		}
		return revision, resp.ContentLength, nil
	}
	revision, _, err := head(files[0].Source)
	if err != nil || len(revision) != 40 {
		return nil, false, nil
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return nil, false, nil
	}
	batch := append([]BatchFile(nil), files...)
	for i, u := range parsed {
		parts := strings.SplitN(strings.TrimPrefix(u.EscapedPath(), "/"), "/", 5)
		parts[3] = revision
		remote := "https://huggingface.co/" + strings.Join(parts, "/")
		got, size, err := head(remote)
		if err != nil {
			return nil, true, err
		}
		if size < 4 || got != "" && got != revision {
			return nil, true, errors.New("invalid immutable model shard metadata")
		}
		batch[i].Source, batch[i].Size = remote, size
	}
	return batch, true, nil
}

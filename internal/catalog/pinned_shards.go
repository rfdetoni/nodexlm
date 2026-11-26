package catalog

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

// Resolve the immutable Hugging Face revision and exact sizes before
// allocating a multi-shard download. Older servers without metadata use
// the existing sequential path; never mix two known revisions.
func (h Hub) downloadPinnedShards(ctx context.Context, repository, dir, directory string, parts []string, progress func(int64, int64)) (string, bool, error) {
	client := h.Client
	if client == nil {
		client = safedownload.Client()
	}
	remoteFor := func(name string) (string, error) {
		if dir != "" {
			name = path.Join(dir, name)
		}
		return ModelURL(repository, name)
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
	first, err := remoteFor(parts[0])
	if err != nil {
		return "", true, err
	}
	revision, _, err := head(first)
	if err != nil || len(revision) != 40 {
		return "", false, nil
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return "", false, nil
	}
	batch := make([]safedownload.BatchFile, 0, len(parts))
	for _, name := range parts {
		target := filepath.Join(directory, name)
		if _, err := os.Lstat(target); err == nil {
			return "", true, errors.New("model shard already exists")
		} else if !os.IsNotExist(err) {
			return "", true, err
		}
		remote, err := remoteFor(name)
		if err != nil {
			return "", true, err
		}
		remote = strings.Replace(remote, "/resolve/main/", "/resolve/"+revision+"/", 1)
		got, size, err := head(remote)
		if err != nil {
			return "", true, err
		}
		if size < 4 || got != "" && got != revision {
			return "", true, errors.New("invalid immutable model shard metadata")
		}
		batch = append(batch, safedownload.BatchFile{Source: remote, Target: safedownload.StagePath(directory, remote), Size: size})
	}
	if err := safedownload.FetchBatch(ctx, client, batch, 150<<30, progress); err != nil {
		return "", true, err
	}
	// Publish the first shard last, so model discovery cannot see an
	// incomplete set. Retain all verified stages on a publication failure.
	var created []string
	for i := len(parts) - 1; i >= 0; i-- {
		target := filepath.Join(directory, parts[i])
		if err := PublishFile(batch[i].Target, target); err != nil {
			for _, p := range created {
				_ = os.Remove(p)
			}
			return "", true, err
		}
		created = append(created, target)
	}
	for _, f := range batch {
		_ = os.Remove(f.Target)
		_ = os.Remove(f.Target + ".json")
	}
	return filepath.Join(directory, parts[0]), true, nil
}

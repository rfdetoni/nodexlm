package catalog

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"

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
		batch = append(batch, safedownload.BatchFile{Source: remote, Target: safedownload.StagePath(directory, remote)})
	}
	batch, handled, err := safedownload.PlanHuggingFaceBatch(ctx, client, batch)
	if !handled || err != nil {
		return "", handled, err
	}
	if err := safedownload.FetchBatch(ctx, client, batch, 150<<30, progress); err != nil {
		return "", true, err
	}
	// Publish the first shard last, so model discovery cannot see an
	// incomplete set. Retain all verified stages on a publication failure.
	var created []string
	for i := len(parts) - 1; i >= 0; i-- {
		target := filepath.Join(directory, parts[i])
		if err := PublishDownloaded(ctx, batch[i].Target, target); err != nil {
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

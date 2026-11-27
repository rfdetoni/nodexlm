package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/rfdetoni/nodexlm/internal/catalog"
	"github.com/rfdetoni/nodexlm/internal/local"
)

type benchmarkHashReader struct {
	ctx context.Context
	r   io.Reader
}

func (r benchmarkHashReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Explicit opt-in: hashes all model shards with bounded memory.
func (m *Manager) benchmarkModelIdentity(ctx context.Context, model string) (map[string]any, error) {
	path, err := modelPath(m.modelDir, model)
	if err != nil {
		return nil, err
	}
	meta, err := local.InspectGGUF(path)
	if err != nil {
		return nil, err
	}
	names, err := catalog.ShardNames(model)
	if err != nil {
		return nil, err
	}
	parts := make([]map[string]any, 0, len(names))
	buffer := make([]byte, 128<<10)
	for _, name := range names {
		part, err := benchmarkHashPart(ctx, filepath.Join(m.modelDir, name), buffer)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return map[string]any{"model": model, "architecture": meta.Architecture, "parts": parts}, nil
}

func benchmarkHashPart(ctx context.Context, path string, buffer []byte) (map[string]any, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 4 {
		return nil, errors.New("invalid model shard")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("model shard changed before hashing")
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, benchmarkHashReader{ctx: ctx, r: io.LimitReader(file, before.Size()+1)}, buffer)
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || n != before.Size() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("model shard changed during hashing")
	}
	return map[string]any{"name": filepath.Base(path), "size_bytes": n, "sha256": hex.EncodeToString(h.Sum(nil))}, nil
}

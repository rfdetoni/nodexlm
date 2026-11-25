// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rfdetoni/nodexlm/internal/catalog"
	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

const handoffMaxBytes = 150 << 30
const handoffManifestLimit = 64 << 10

type handoffPart struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type handoffManifest struct {
	Version int           `json:"version"`
	Model   string        `json:"model"`
	Parts   []handoffPart `json:"parts"`
}

func handoffNames(model string) ([]string, error) {
	if !filepath.IsLocal(model) || filepath.Base(model) != model || len(model) > 180 || strings.ContainsAny(model, "/\\:") {
		return nil, errors.New("model must be a local GGUF basename")
	}
	for _, ch := range model {
		if ch < 32 || ch == 127 {
			return nil, errors.New("invalid model filename")
		}
	}
	return catalog.ShardNames(model)
}

type handoffReader struct {
	ctx context.Context
	r   io.Reader
}

func (r handoffReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func openHandoffPart(path string) (*os.File, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() < 4 || info.Size() > handoffMaxBytes {
		return nil, 0, errors.New("GGUF must be a regular non-symlink file within the size limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) || actual.Size() != info.Size() {
		f.Close()
		return nil, 0, errors.New("source changed while opening")
	}
	var magic [4]byte
	if _, err = f.ReadAt(magic[:], 0); err != nil || string(magic[:]) != "GGUF" {
		f.Close()
		return nil, 0, errors.New("invalid GGUF header")
	}
	return f, info.Size(), nil
}

func hashHandoffPart(ctx context.Context, path string) (handoffPart, error) {
	f, size, err := openHandoffPart(path)
	if err != nil {
		return handoffPart{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, handoffReader{ctx, io.LimitReader(f, size+1)})
	if err != nil {
		return handoffPart{}, err
	}
	if n != size {
		return handoffPart{}, errors.New("GGUF changed size during hashing")
	}
	return handoffPart{filepath.Base(path), size, hex.EncodeToString(h.Sum(nil))}, nil
}

func makeHandoffManifest(ctx context.Context, source string) (handoffManifest, error) {
	model := filepath.Base(source)
	names, err := handoffNames(model)
	if err != nil {
		return handoffManifest{}, err
	}
	m := handoffManifest{Version: 1, Model: model}
	var total int64
	for _, name := range names {
		p, err := hashHandoffPart(ctx, filepath.Join(filepath.Dir(source), name))
		if err != nil {
			return handoffManifest{}, fmt.Errorf("hash %s: %w", name, err)
		}
		total += p.Size
		if total > handoffMaxBytes {
			return handoffManifest{}, errors.New("model exceeds handoff size limit")
		}
		m.Parts = append(m.Parts, p)
	}
	return m, nil
}

func validateHandoffManifest(m handoffManifest) (int64, error) {
	names, err := handoffNames(m.Model)
	if err != nil || m.Version != 1 || len(names) != len(m.Parts) {
		return 0, errors.New("invalid or incomplete handoff manifest")
	}
	var total int64
	for i, p := range m.Parts {
		digest, err := hex.DecodeString(p.SHA256)
		if p.Name != names[i] || p.Size < 4 || p.Size > handoffMaxBytes-total ||
			err != nil || len(digest) != sha256.Size || p.SHA256 != strings.ToLower(p.SHA256) {
			return 0, errors.New("invalid handoff part")
		}
		total += p.Size
	}
	return total, nil
}

func copyHandoffPart(ctx context.Context, source, dest string, expected handoffPart) error {
	in, size, err := openHandoffPart(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if size != expected.Size {
		return errors.New("source size does not match manifest")
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), handoffReader{ctx, io.LimitReader(in, size+1)})
	if copyErr == nil && (n != size || hex.EncodeToString(h.Sum(nil)) != expected.SHA256) {
		copyErr = errors.New("source digest does not match manifest")
	}
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	actual, err := hashHandoffPart(ctx, dest)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("destination digest does not match manifest")
	}
	return nil
}

// stageHandoff creates a fresh Host home with only verified GGUFs. It never
// copies credentials or changes the running coordinator/Guest role.
func stageHandoff(ctx context.Context, source, target string, m handoffManifest) error {
	total, err := validateHandoffManifest(m)
	if err != nil {
		return err
	}
	if filepath.Base(source) != m.Model {
		return errors.New("source must name the first manifest shard")
	}
	free, err := safedownload.FreeBytes(filepath.Dir(target))
	if err != nil {
		return err
	}
	if free < uint64(total) {
		return errors.New("insufficient destination disk space")
	}
	if err := os.Mkdir(target, 0700); err != nil {
		return fmt.Errorf("target must be a fresh directory: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			// The fresh directory belongs exclusively to this operation.
			_ = os.RemoveAll(target)
		}
	}()
	modelDir := filepath.Join(target, "models")
	if err := os.Mkdir(modelDir, 0700); err != nil {
		return err
	}
	for _, part := range m.Parts {
		if err := copyHandoffPart(ctx, filepath.Join(filepath.Dir(source), part.Name), filepath.Join(modelDir, part.Name), part); err != nil {
			return fmt.Errorf("stage %s: %w", part.Name, err)
		}
	}
	complete = true
	return nil
}

func handoff(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: nodexlm handoff manifest|stage")
	}
	f := fs("handoff " + args[0])
	source := f.String("source", "", "first GGUF or single model on source/verified mount")
	manifest := f.String("manifest", "", "trusted manifest JSON from old Host")
	target := f.String("target", "", "new, non-existing Host home directory")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if *source == "" || f.NArg() != 0 {
		return errors.New("--source required; unexpected positional arguments are not supported")
	}
	c, cancel := ctx()
	defer cancel()
	switch args[0] {
	case "manifest":
		m, err := makeHandoffManifest(c, *source)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(m)
	case "stage":
		if *manifest == "" || *target == "" {
			return errors.New("--manifest and --target required")
		}
		file, err := os.Open(*manifest)
		if err != nil {
			return err
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, handoffManifestLimit+1))
		if err != nil {
			return err
		}
		if len(raw) > handoffManifestLimit {
			return errors.New("handoff manifest too large")
		}
		var m handoffManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if err := stageHandoff(c, *source, *target, m); err != nil {
			return err
		}
		fmt.Println("Verified GGUF set staged. Stop old Host and target Guest before starting the new Host with --dir pointing to the target. Re-pair Guests with the new identity.")
		return nil
	default:
		return errors.New("usage: nodexlm handoff manifest|stage")
	}
}

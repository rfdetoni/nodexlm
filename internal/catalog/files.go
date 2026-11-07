// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

// ModelURL constructs only a public Hugging Face GGUF resolve URL. The
// repository and file names are validated instead of accepting arbitrary URLs.
func ModelURL(repository, file string) (string, error) {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || !safePart(parts[0]) || !safePart(parts[1]) {
		return "", errors.New("repository must be owner/name")
	}
	segments := strings.Split(file, "/")
	if len(segments) == 0 || len(segments) > 20 || !strings.EqualFold(path.Ext(file), ".gguf") {
		return "", errors.New("only single GGUF files are supported")
	}
	for _, segment := range segments {
		if !safePart(segment) {
			return "", errors.New("invalid model filename")
		}
	}
	escaped := make([]string, len(segments))
	for i, segment := range segments {
		escaped[i] = url.PathEscape(segment)
	}
	return fmt.Sprintf("https://huggingface.co/%s/%s/resolve/main/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]), strings.Join(escaped, "/")), nil
}

func safePart(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 180 {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 || c == '/' || c == '\\' || c == '?' || c == '#' || c == '%' || c == ':' {
			return false
		}
	}
	return true
}

func safeName(name string) bool {
	return safePart(name) && strings.EqualFold(filepath.Ext(name), ".gguf")
}

// DownloadFile imports the complete GGUF set when filename references the
// first shard. It never leaves a partially published set after failure.
func (h Hub) DownloadFile(ctx context.Context, repository, filename, directory, name string, progress func(int64, int64)) (string, error) {

	remote, err := ModelURL(repository, filename)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	release, err := safedownload.LockTransfer(safedownload.StagePath(directory, remote) + ".lock")
	if err != nil {
		return "", err
	}
	defer release()
	parts, err := ShardNames(path.Base(filename))
	if err != nil {
		return "", err
	}
	if len(parts) == 1 {
		return h.downloadSingle(ctx, repository, filename, directory, name, 150<<30, progress)
	}
	if name != "" && name != parts[0] {
		return "", errors.New("sharded GGUF destination must use the original first-shard filename")
	}
	dir := path.Dir(filename)
	if dir == "." {
		dir = ""
	}
	created := make([]string, 0, len(parts))
	var received int64
	for _, shard := range parts {
		location := shard
		if dir != "" {
			location = path.Join(dir, shard)
		}
		target, err := h.downloadSingle(ctx, repository, location, directory, shard, (150<<30)-received, func(n, total int64) {
			if progress != nil {
				progress(received+n, -1)
			}
		})
		if err != nil {
			for _, previous := range created {
				_ = os.Remove(previous)
			}
			return "", fmt.Errorf("shard %s: %w", shard, err)
		}
		created = append(created, target)
		info, err := os.Stat(target)
		if err != nil {
			for _, previous := range created {
				_ = os.Remove(previous)
			}
			return "", err
		}
		received += info.Size()
	}
	if progress != nil {
		progress(received, received)
	}
	return created[0], nil
}

// downloadSingle stages and checks one GGUF without buffering model weights.
func (h Hub) downloadSingle(ctx context.Context, repository, filename, directory, name string, limit int64, progress func(int64, int64)) (string, error) {
	remote, err := ModelURL(repository, filename)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = path.Base(filename)
	}
	if !safeName(name) {
		return "", errors.New("invalid destination GGUF filename")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	destination := filepath.Join(directory, name)
	if _, err := os.Lstat(destination); err == nil {
		return "", errors.New("model already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	stage := safedownload.StagePath(directory, remote)
	if _, err := safedownload.Fetch(ctx, h.Client, remote, stage, limit, progress); err != nil {
		return "", err
	}
	if err := PublishFile(stage, destination); err != nil {
		return "", err
	}
	_ = os.Remove(stage)
	_ = os.Remove(stage + ".json")
	return destination, nil
}

func verifyGGUF(f *os.File) error {
	var b [4]byte
	if _, err := f.ReadAt(b[:], 0); err != nil {
		return errors.New("truncated model header")
	}
	if string(b[:]) != "GGUF" {
		return errors.New("download is not a GGUF file")
	}
	return nil
}

// ImportFile accepts one GGUF or an entire complete shard set next to the
// selected first shard. Partial imports are rolled back on failure.
func ImportFile(ctx context.Context, source, directory, name string) (string, error) {
	parts, err := ShardNames(filepath.Base(source))
	if err != nil {
		return "", err
	}
	if len(parts) == 1 {
		return importSingle(ctx, source, directory, name)
	}
	if name != "" && name != parts[0] {
		return "", errors.New("sharded import cannot rename individual shard files")
	}
	created := make([]string, 0, len(parts))
	for _, shard := range parts {
		location := filepath.Join(filepath.Dir(source), shard)
		target, err := importSingle(ctx, location, directory, shard)
		if err != nil {
			for _, previous := range created {
				_ = os.Remove(previous)
			}
			return "", fmt.Errorf("shard %s: %w", shard, err)
		}
		created = append(created, target)
	}
	return created[0], nil
}

func importSingle(ctx context.Context, source, directory, name string) (string, error) {
	src, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("source must be a regular GGUF file")
	}
	if err = verifyGGUF(src); err != nil {
		return "", err
	}
	if name == "" {
		name = filepath.Base(source)
	}
	if !safeName(name) {
		return "", errors.New("invalid destination GGUF filename")
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	destination := filepath.Join(directory, name)
	if _, err = os.Lstat(destination); err == nil {
		return "", errors.New("model already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tmp, err := os.CreateTemp(directory, ".nodexlm-import-*.part")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err = tmp.Chmod(0600); err != nil {
		return "", err
	}
	if _, err = io.Copy(tmp, &readerWithContext{ctx: ctx, r: src}); err != nil {
		return "", err
	}
	if err = tmp.Sync(); err != nil {
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	if err = PublishFile(tmp.Name(), destination); err != nil {
		return "", err
	}
	return destination, nil
}

// PublishFile prefers atomic hardlink publication; filesystems without links
// use a no-clobber O_EXCL copy and remove an incomplete destination on error.
func PublishFile(source, destination string) error {
	if err := os.Link(source, destination); err == nil {
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	return nil
}

type readerWithContext struct {
	ctx context.Context
	r   io.Reader
}

func (c *readerWithContext) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

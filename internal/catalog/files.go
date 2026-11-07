// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package catalog

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	parts, err := ShardNames(path.Base(filename))
	if err != nil {
		return "", err
	}
	if len(parts) == 1 {
		return h.downloadSingle(ctx, repository, filename, directory, name, progress)
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
		target, err := h.downloadSingle(ctx, repository, location, directory, shard, func(n, total int64) {
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
func (h Hub) downloadSingle(ctx context.Context, repository, filename, directory, name string, progress func(int64, int64)) (string, error) {
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
	file, err := os.CreateTemp(directory, ".nodexlm-hub-*.part")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NodexLM/0.1 (GGUF downloader)")
	client := h.Client
	if client == nil {
		client = safedownload.Client()
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Hugging Face download HTTP %d", res.StatusCode)
	}
	const limit int64 = 150 << 30
	if res.ContentLength > limit {
		return "", errors.New("file exceeds 150 GiB")
	}
	var count int64
	digest := sha256.New()
	buf := make([]byte, 256<<10)
	for {
		n, readErr := res.Body.Read(buf)
		if n > 0 {
			count += int64(n)
			if count > limit {
				return "", errors.New("file exceeds 150 GiB")
			}
			if _, err = file.Write(buf[:n]); err != nil {
				return "", err
			}
			if _, err = digest.Write(buf[:n]); err != nil {
				return "", err
			}
			if progress != nil {
				progress(count, res.ContentLength)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
		if err = ctx.Err(); err != nil {
			return "", err
		}
	}
	if res.ContentLength >= 0 && count != res.ContentLength {
		return "", errors.New("incomplete download")
	}
	if expected, ok := safedownload.ExpectedSHA256(res.Header); ok && subtle.ConstantTimeCompare(digest.Sum(nil), expected) != 1 {
		return "", errors.New("download SHA-256 does not match repository metadata")
	}
	if err = verifyGGUF(file); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	// Do not overwrite a model that was created concurrently.
	if err = publishFile(file.Name(), destination); err != nil {
		return "", err
	}
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
	if err = publishFile(tmp.Name(), destination); err != nil {
		return "", err
	}
	return destination, nil
}

// publishFile prefers atomic hardlink publication; filesystems without links
// use a no-clobber O_EXCL copy and remove an incomplete destination on error.
func publishFile(source, destination string) error {
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

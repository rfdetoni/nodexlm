package safedownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// OpenVerifiedContent opens a previously published immutable model from the
// local SHA-256 index. It never follows symbolic links or trusts stale index
// entries. The caller must authenticate before asking for file content.
func OpenVerifiedContent(directory, digest string) (*os.File, error) {
	if !digestName.MatchString(digest) {
		return nil, errors.New("invalid cached content digest")
	}
	ctx := WithContentCache(context.Background(), directory)
	index := cacheIndex(ctx, digest, false)
	if index == "" {
		return nil, errors.New("content cache unavailable")
	}
	info, err := os.Lstat(index)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024 {
		return nil, errors.New("content cache index missing")
	}
	raw, err := os.ReadFile(index)
	if err != nil {
		return nil, err
	}
	name := string(raw)
	if name == "" || name != filepath.Base(name) || !strings.EqualFold(filepath.Ext(name), ".gguf") {
		return nil, errors.New("invalid cached filename")
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	candidate := filepath.Join(root, name)
	before, err := os.Lstat(candidate)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 8 || before.Size() > 150<<30 {
		return nil, errors.New("cached model not a bounded regular file")
	}
	file, err := os.Open(candidate)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.New("cached model changed while opening")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return nil, errors.New("stale cached model digest")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		return nil, errors.New("cached model changed during validation")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	closeOnError = false
	return file, nil
}

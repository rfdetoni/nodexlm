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
	"sync"
)

// Verified model identities are cached by inode, size, and modification
// time. This avoids rehashing many gigabytes for every trusted LAN consumer.
// Recipients still verify the complete SHA-256 before publishing model files.
type verifiedModel struct {
	info os.FileInfo
}
var verifiedModels = struct {
	sync.Mutex
	files map[string]verifiedModel
}{files: make(map[string]verifiedModel)}

func verifiedKey(path, digest string) string {
	return path + "\x00" + digest
}

func alreadyVerified(path, digest string, current os.FileInfo) bool {
	key := verifiedKey(path, digest)
	verifiedModels.Lock()
	defer verifiedModels.Unlock()
	previous, ok := verifiedModels.files[key]
	if !ok {
		return false
	}
	if previous.info.Size() != current.Size() || !previous.info.ModTime().Equal(current.ModTime()) ||
		!os.SameFile(previous.info, current) {
		delete(verifiedModels.files, key)
		return false
	}
	return true
}

func rememberVerified(path, digest string, info os.FileInfo) {
	if !digestName.MatchString(digest) || info == nil || !info.Mode().IsRegular() {
		return
	}
	verifiedModels.Lock()
	defer verifiedModels.Unlock()
	if len(verifiedModels.files) >= 64 {
		clear(verifiedModels.files)
	}
	verifiedModels.files[verifiedKey(path, digest)] = verifiedModel{info: info}
}

// OpenVerifiedContent opens a previously published immutable model from the
// local SHA-256 index. It never follows symbolic links or trusts stale index
// entries. The caller must authenticate before asking for file content.
func OpenVerifiedContent(ctx context.Context, directory, digest string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !digestName.MatchString(digest) {
		return nil, errors.New("invalid cached content digest")
	}
	ctx = WithContentCache(ctx, directory)
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
	if !alreadyVerified(candidate, digest, before) {
		hash := sha256.New()
		if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
			return nil, err
		}
		if hex.EncodeToString(hash.Sum(nil)) != digest {
			return nil, errors.New("stale cached model digest")
		}
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		return nil, errors.New("cached model changed during validation")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	rememberVerified(candidate, digest, after)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	closeOnError = false
	return file, nil
}

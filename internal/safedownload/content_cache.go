package safedownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

type cacheDirectoryKey struct{}

var digestName = regexp.MustCompile("^[0-9a-f]{64}$")

// WithContentCache enables verified, local content-addressed reuse in the
// configured model library. Hard links share the inode; enable only for
// trusted immutable model libraries. There is no retained second weight copy.
func WithContentCache(ctx context.Context, directory string) context.Context {
	if os.Getenv("NODEXLM_MODEL_DEDUP") != "1" && os.Getenv("NODEXLM_MODEL_P2P") != "1" {
		return ctx
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, cacheDirectoryKey{}, root)
}

func cacheIndex(ctx context.Context, digest string, create bool) string {
	root, _ := ctx.Value(cacheDirectoryKey{}).(string)
	if root == "" || !digestName.MatchString(digest) {
		return ""
	}
	dir := filepath.Join(root, ".nodexlm-content-index")
	if create {
		if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return ""
		}
	}
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	return filepath.Join(dir, digest)
}

// RememberPublished records only verified SHA-256 identities supplied by Fetch.
// The index contains a basename, not a URL or another copy of the model.
func RememberPublished(ctx context.Context, stage, published string) {
	raw, err := os.ReadFile(stage + ".json")
	if err != nil || len(raw) > 16384 {
		return
	}
	var meta partial
	if json.Unmarshal(raw, &meta) != nil {
		return
	}
	index := cacheIndex(ctx, meta.SHA256, true)
	root, _ := ctx.Value(cacheDirectoryKey{}).(string)
	if index == "" || filepath.Dir(published) != root {
		return
	}
	info, err := os.Lstat(published)
	if err != nil || !info.Mode().IsRegular() || info.Size() != meta.Total {
		return
	}
	temp, err := os.CreateTemp(filepath.Dir(index), ".entry-*")
	if err != nil {
		return
	}
	defer os.Remove(temp.Name())
	_, err = temp.WriteString(filepath.Base(published))
	closeErr := temp.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(temp.Name(), index)
	}
}

// PublishVerifiedLink attempts deduplicated publication from an already
// verified stage. The source index is revalidated byte-for-byte before
// creating the final hard link. No writable partial ever shares the inode.
func PublishVerifiedLink(ctx context.Context, stage, destination string) (bool, error) {
	raw, err := os.ReadFile(stage + ".json")
	if err != nil || len(raw) > 16384 {
		return false, nil
	}
	var meta partial
	if json.Unmarshal(raw, &meta) != nil || meta.Total < 4 {
		return false, nil
	}
	index := cacheIndex(ctx, meta.SHA256, false)
	if index == "" {
		return false, nil
	}
	st, err := os.Lstat(index)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1024 {
		return false, nil
	}
	raw, err = os.ReadFile(index)
	name := string(raw)
	if err != nil || name == "" || name != filepath.Base(name) || filepath.Ext(name) != ".gguf" {
		return false, nil
	}
	root, _ := ctx.Value(cacheDirectoryKey{}).(string)
	source := filepath.Join(root, name)
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() != meta.Total {
		return false, nil
	}
	file, err := os.Open(source)
	if err != nil {
		return false, nil
	}
	defer file.Close()
	current, err := file.Stat()
	if err != nil || !os.SameFile(info, current) {
		return false, nil
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		return false, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != meta.SHA256 {
		return false, nil
	}
	current, err = os.Lstat(source)
	if err != nil || !os.SameFile(info, current) || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := os.Link(source, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, err
		}
		return false, nil
	}
	linked, err := os.Lstat(destination)
	if err != nil || !os.SameFile(info, linked) {
		_ = os.Remove(destination)
		return false, errors.New("content cache changed during publication")
	}
	return true, nil
}

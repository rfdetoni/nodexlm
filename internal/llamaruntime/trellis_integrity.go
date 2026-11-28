package llamaruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const trellisIntegrityManifest = "runtime-integrity.sha256"

// Both the Go and shell installers produce the same relative-path manifest.
// Include every binary/library so unexpected loader inputs cannot go unnoticed.
func trellisInstallDigests(root string) (map[string]string, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("Trellis installation must be a real directory")
	}
	info, err = os.Lstat(filepath.Join(root, "bin"))
	if err != nil || !info.IsDir() {
		return nil, errors.New("Trellis bin must be a real directory")
	}
	entries, err := os.ReadDir(filepath.Join(root, "bin"))
	if err != nil {
		return nil, err
	}
	names := []string{"source-commit.txt", "LICENSE"}
	for _, entry := range entries {
		if entry.IsDir() || strings.ContainsAny(entry.Name(), "\\\r\n") {
			return nil, errors.New("unexpected Trellis runtime entry")
		}
		names = append(names, "bin/"+entry.Name())
	}
	out := make(map[string]string, len(names))
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("invalid Trellis runtime file %q", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		after, statErr := file.Stat()
		closeErr := file.Close()
		if err := errors.Join(copyErr, statErr, closeErr); err != nil {
			return nil, err
		}
		if !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
			return nil, errors.New("Trellis runtime changed during verification")
		}
		out[name] = hex.EncodeToString(hash.Sum(nil))
	}
	return out, nil
}

func sealTrellisInstall(root string) error {
	digests, err := trellisInstallDigests(root)
	if err != nil {
		return err
	}
	var lines []string
	for name, digest := range digests {
		lines = append(lines, digest+"  "+name)
	}
	sort.Strings(lines)
	return os.WriteFile(filepath.Join(root, trellisIntegrityManifest), []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

func verifyTrellisInstall(root string) error {
	marker, err := os.ReadFile(filepath.Join(root, "source-commit.txt"))
	if err != nil || strings.TrimSpace(string(marker)) != TrellisSourceCommit {
		return errors.New("Trellis installation source revision missing or incompatible")
	}
	manifest := filepath.Join(root, trellisIntegrityManifest)
	info, err := os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return errors.New("Trellis runtime integrity manifest missing or invalid")
	}
	raw, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	expected := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		digest, name, ok := strings.Cut(line, "  ")
		decoded, decodeErr := hex.DecodeString(digest)
		if !ok || decodeErr != nil || len(decoded) != sha256.Size || expected[name] != "" {
			return errors.New("invalid Trellis runtime integrity manifest entry")
		}
		expected[name] = digest
	}
	actual, err := trellisInstallDigests(root)
	if err != nil {
		return err
	}
	if len(expected) != len(actual) {
		return errors.New("Trellis runtime file inventory changed")
	}
	for name, digest := range actual {
		if expected[name] != digest {
			return fmt.Errorf("Trellis runtime SHA-256 mismatch: %s", name)
		}
	}
	return nil
}

package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

const releaseOrigin = "https://github.com/rfdetoni/nodexlm/releases/download/"
const releaseAPI = "https://api.github.com/repos/rfdetoni/nodexlm/releases/tags/"
const maxGuestArchive = 128 << 20

// Only the official release channel and a newer version may be installed.
// Never turn the Host directive into shell code or a download URL.
func guestUpdateTag(current, desired string) (string, error) {
	edge := func(v string) (int, bool) {
		n, err := strconv.Atoi(strings.TrimPrefix(v, "0.1.0-edge."))
		return n, strings.HasPrefix(v, "0.1.0-edge.") && err == nil && n > 0
	}
	if next, ok := edge(desired); ok {
		if old, ok := edge(current); ok && next <= old {
			return "", errors.New("Host is not newer than Guest; update the Host")
		}
		if current != "0.1.0-alpha" {
			if _, ok := edge(current); !ok {
				return "", errors.New("refusing to switch stable Guest to edge automatically")
			}
		}
		return "nodexlm-edge", nil
	}
	parse := func(v string) ([3]int, bool) {
		var result [3]int
		parts := strings.Split(v, ".")
		if len(parts) != 3 {
			return result, false
		}
		for i, part := range parts {
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 || strconv.Itoa(n) != part {
				return result, false
			}
			result[i] = n
		}
		return result, true
	}
	next, ok := parse(desired)
	if !ok {
		return "", errors.New("unsupported Host release version")
	}
	if old, ok := parse(current); ok {
		newer := false
		for i := range old {
			if next[i] != old[i] {
				newer = next[i] > old[i]
				break
			}
		}
		if !newer {
			return "", errors.New("refusing a same-version or older stable release")
		}
	} else if _, ok := edge(current); !ok && current != "0.1.0-alpha" {
		return "", errors.New("unsupported installed release channel")
	}
	return "v" + desired, nil
}

func guestAsset() (string, string, error) {
	arch := runtime.GOARCH
	if arch == "arm" {
		arch = "armv7"
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return "", "", errors.New("unsupported automatic desktop update platform")
	}
	name := "nodexlm"
	suffix := ".tar.gz"
	if runtime.GOOS == "windows" {
		name += ".exe"
		suffix = ".zip"
	}
	return "nodexlm_" + runtime.GOOS + "_" + arch + suffix, name, nil
}

func downloadGuestUpdate(ctx context.Context, directive protocol.UpdateDirective, dir string) (string, error) {
	if !protocol.ValidBuildCommit(directive.RequiredBuildCommit) {
		return "", errors.New("invalid Host release identity")
	}
	tag, err := guestUpdateTag(version, directive.RequiredVersion)
	if err != nil {
		return "", err
	}
	asset, name, err := guestAsset()
	if err != nil {
		return "", err
	}
	client := safedownload.Client()
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI+tag, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Draft  bool   `json:"draft"`
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&release)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || decodeErr != nil || release.Draft || release.Tag != tag {
		return "", errors.New("required official desktop release not available")
	}
	archive := filepath.Join(dir, asset)
	for _, a := range release.Assets {
		if a.Name != asset {
			continue
		}
		digest, err := hex.DecodeString(strings.TrimPrefix(a.Digest, "sha256:"))
		if err != nil || !strings.HasPrefix(a.Digest, "sha256:") || len(digest) != sha256.Size || a.URL != releaseOrigin+tag+"/"+asset || a.Size <= 0 || a.Size > maxGuestArchive {
			return "", errors.New("invalid official update asset metadata")
		}
		if n, err := safedownload.Fetch(ctx, client, a.URL, archive, maxGuestArchive, nil); err != nil || n != a.Size {
			return "", fmt.Errorf("desktop update download incomplete: %v", err)
		}
		f, err := os.Open(archive)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, f)
		f.Close()
		if err != nil || hex.EncodeToString(hash.Sum(nil)) != hex.EncodeToString(digest) {
			return "", errors.New("desktop update SHA-256 mismatch")
		}
		candidate := filepath.Join(dir, name)
		if err := extractGuestBinary(archive, name, candidate); err != nil {
			return "", err
		}
		check, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := exec.CommandContext(check, candidate, "build-commit").Output()
		cancel()
		if err != nil || strings.TrimSpace(string(out)) != directive.RequiredBuildCommit {
			return "", errors.New("published asset does not yet match Host commit")
		}
		check, cancel = context.WithTimeout(ctx, 10*time.Second)
		out, err = exec.CommandContext(check, candidate, "version").Output()
		cancel()
		if err != nil || strings.TrimSpace(string(out)) != "nodexlm "+directive.RequiredVersion {
			return "", errors.New("published asset does not match Host version")
		}
		return candidate, nil
	}
	return "", errors.New("official release has no asset for this Guest")
}

func extractGuestBinary(archive, name, destination string) error {
	write := func(r io.Reader, size int64) error {
		if size <= 0 || size > maxGuestArchive {
			return errors.New("invalid executable size")
		}
		f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if err != nil {
			return err
		}
		_, err = io.CopyN(f, r, size)
		return errors.Join(err, f.Sync(), f.Close())
	}
	if strings.HasSuffix(archive, ".zip") {
		reader, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.Name == name && file.Mode().IsRegular() && file.UncompressedSize64 <= maxGuestArchive {
				r, err := file.Open()
				if err != nil {
					return err
				}
				defer r.Close()
				return write(r, int64(file.UncompressedSize64))
			}
		}
	} else {
		file, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if header.Name == name && header.Typeflag == tar.TypeReg {
				return write(reader, header.Size)
			}
		}
	}
	return errors.New("release archive lacks the expected executable")
}

func applyGuestUpdate(ctx context.Context, directive protocol.UpdateDirective, args []string) error {
	if !directive.UpdateRequired || !protocol.ValidBuildCommit(directive.RequiredBuildCommit) || directive.RequiredBuildCommit == buildCommit {
		return errors.New("invalid update directive")
	}
	if _, err := guestUpdateTag(version, directive.RequiredVersion); err != nil {
		return err
	}
	current, err := os.Executable()
	if err != nil {
		return err
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return err
	}
	unlock, err := safedownload.LockTransfer(filepath.Join(filepath.Dir(current), ".nodexlm-update.lock"))
	if err != nil {
		return err
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	stage, err := os.MkdirTemp(filepath.Dir(current), ".nodexlm-update-")
	if err != nil {
		return fmt.Errorf("Guest installation is not writable; update using its package manager: %w", err)
	}
	defer os.RemoveAll(stage)
	var candidate string
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 10*time.Minute)
		candidate, err = downloadGuestUpdate(attempt, directive, stage)
		cancel()
		if err == nil {
			break
		}
		slog.Warn("Waiting for compatible official Guest update", "error", err)
		entries, _ := os.ReadDir(stage)
		for _, entry := range entries {
			_ = os.RemoveAll(filepath.Join(stage, entry.Name()))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Minute):
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Replace only the current binary; pairing state is never touched.
	// Windows may reject renaming an in-use executable; fail closed if so.
	backup := filepath.Join(stage, "previous-"+filepath.Base(current))
	if err := os.Rename(current, backup); err != nil {
		return err
	}
	if err := os.Rename(candidate, current); err != nil {
		return errors.Join(err, os.Rename(backup, current))
	}
	child := exec.CommandContext(ctx, current, args...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		_ = os.Remove(current)
		return errors.Join(err, os.Rename(backup, current))
	}
	slog.Info("Guest updated; reconnecting with saved pairing", "version", directive.RequiredVersion)
	unlock()
	locked = false
	// The child can subsequently update the installation independently.
	return child.Wait()
}

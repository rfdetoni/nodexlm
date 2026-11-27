package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

const flashNextPLE = "Qwen3.8-Flash-Next-PLE-Q8_0.gguf"
const flashNextPLESHA256 = "eb555a6de2fbebeff59441204fc8ed9c1ee4f958e5f01afc1d249f54e3793d12"
const flashNextPLEURL = "https://huggingface.co/Delgon/Qwen3.8-Flash-Next-PLE-GGUF/resolve/main/" + flashNextPLE

// Only the documented Flash-Next noPLE sidecar is auto-selected.
func trellisPLEPath(model string, meta GGUFInfo) (string, error) {
	if !strings.Contains(strings.ToLower(filepath.Base(model)), "-nople") {
		return "", nil
	}
	if meta.Architecture != "qwen4exp" || !strings.Contains(strings.ToLower(filepath.Base(model)), "qwen3.8-flash-next") {
		return "", errors.New("unknown noPLE model; explicit compatible PLE support is required")
	}
	return filepath.Join(filepath.Dir(model), flashNextPLE), nil
}

func TrellisPLE(model string, meta GGUFInfo) (string, error) {
	path, err := trellisPLEPath(model, meta)
	if err != nil || path == "" {
		return path, err
	}
	if _, err := ModelSize(path); err != nil {
		return "", fmt.Errorf("missing or invalid external PLE table %s; download from Delgon/Qwen3.8-Flash-Next-PLE-GGUF: %w", flashNextPLE, err)
	}
	if _, err := InspectGGUF(path); err != nil {
		return "", fmt.Errorf("invalid external PLE metadata: %w", err)
	}
	return path, nil
}

// EnsureTrellisPLE downloads only a reviewed dependency with an exact digest.
// The 54 GB transfer is cancellable, disk-admitted and resumable. Never
// publish a partial or unverified GGUF into the model directory.
func EnsureTrellisPLE(ctx context.Context, model string, meta GGUFInfo, progress func(int64, int64)) (string, error) {
	path, err := trellisPLEPath(model, meta)
	if err != nil || path == "" {
		return path, err
	}
	if _, err := os.Lstat(path); err == nil {
		return TrellisPLE(model, meta)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	stage := safedownload.StagePath(filepath.Dir(path), flashNextPLEURL) + ".ple"
	if err := os.MkdirAll(filepath.Dir(stage), 0700); err != nil {
		return "", err
	}
	unlock, err := safedownload.LockTransfer(stage + ".lock")
	if err != nil {
		return "", err
	}
	defer unlock()
	if _, err := os.Lstat(path); err == nil {
		return TrellisPLE(model, meta)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := safedownload.Fetch(ctx, nil, flashNextPLEURL, stage, 60<<30, progress); err != nil {
		return "", err
	}
	if err := verifyTrellisPLE(ctx, stage); err != nil {
		_ = os.Remove(stage)
		_ = os.Remove(stage + ".json")
		return "", err
	}
	if _, err := InspectGGUF(stage); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("PLE destination appeared during download")
	}
	if err := os.Rename(stage, path); err != nil {
		return "", err
	}
	return TrellisPLE(model, meta)
}

func verifyTrellisPLE(ctx context.Context, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	buf := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := file.Read(buf)
		if n > 0 {
			_, _ = hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != flashNextPLESHA256 {
		return errors.New("external PLE SHA-256 mismatch; refusing to load")
	}
	return nil
}

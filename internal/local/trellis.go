package local

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const flashNextPLE = "Qwen3.8-Flash-Next-PLE-Q8_0.gguf"

// Only the documented Flash-Next noPLE sidecar is auto-selected. Never
// substitute an arbitrary GGUF or download model data during native startup.
func TrellisPLE(model string, meta GGUFInfo) (string, error) {
	if !strings.Contains(strings.ToLower(filepath.Base(model)), "-nople") {
		return "", nil
	}
	if meta.Architecture != "qwen4exp" || !strings.Contains(strings.ToLower(filepath.Base(model)), "qwen3.8-flash-next") {
		return "", errors.New("unknown noPLE model; explicit compatible PLE support is required")
	}
	path := filepath.Join(filepath.Dir(model), flashNextPLE)
	if _, err := ModelSize(path); err != nil {
		return "", fmt.Errorf("missing or invalid external PLE table %s; download from Delgon/Qwen3.8-Flash-Next-PLE-GGUF: %w", flashNextPLE, err)
	}
	if _, err := InspectGGUF(path); err != nil {
		return "", fmt.Errorf("invalid external PLE metadata: %w", err)
	}
	return path, nil
}

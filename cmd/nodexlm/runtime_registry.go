package main

import (
	"context"
	"errors"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
)

// CLI and lazy whole-model workers use the same format admission as the Host.
func resolveCLIModelRuntime(ctx context.Context, home, binary, path string, forceTrellis bool) (string, bool, error) {
	if binary != "" && binary != "llama-server" && !forceTrellis {
		return binary, false, nil
	}
	if !forceTrellis {
		_, err := local.InspectBundledGGUF(path)
		if err == nil {
			resolved, err := llamaruntime.AutoExecutable(ctx, home, binary)
			return resolved, false, err
		}
		if !errors.Is(err, llamaruntime.ErrTrellisFormat) {
			return "", false, err
		}
	}
	meta, err := local.InspectTrellisGGUF(path)
	if err != nil {
		return "", false, err
	}
	if _, err := local.TrellisPLE(path, meta); err != nil {
		return "", false, err
	}
	resolved, err := llamaruntime.ResolveTrellis(home, "auto")
	return resolved, true, err
}

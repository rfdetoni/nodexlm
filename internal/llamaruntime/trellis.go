package llamaruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Separate candidate; never substitute these libraries into the stock bundle.
const TrellisSourceCommit = "dc5521842f7991ceae73139f9d242d8f877e12d4"

func ResolveTrellis(home, backend string) (string, error) {
	if backend == "" || backend == "auto" {
		backend = "cpu"
	}
	switch backend {
	case "cpu", "vulkan", "cuda", "metal":
	default:
		return "", errors.New("unsupported Trellis backend")
	}
	path := os.Getenv("NODEXLM_TRELLIS_BIN")
	if path == "" {
		path = filepath.Join(home, "runtimes", "trellis", TrellisSourceCommit, backend, "bin", exe("llama-server"))
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("NODEXLM_TRELLIS_BIN must be an absolute local path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Trellis runtime missing for %s; run scripts/install-trellis-runtime.sh %s on the Host or set NODEXLM_TRELLIS_BIN to a compatible local llama-server", backend, backend)
	}
	return path, nil
}

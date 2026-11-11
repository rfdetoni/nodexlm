package llamaruntime

import (
	"os"
	"path/filepath"
	"runtime"
)

// NativeEnvironment exposes the verified runtime's sibling GGML shared
// libraries to both capability probes and the launched llama-server.
// The PATH is process-local: it does not alter the user's global environment.
func NativeEnvironment(binary string) []string {
	env := os.Environ()
	dir := filepath.Dir(binary)
	switch runtime.GOOS {
	case "linux":
		path := dir
		if inherited := os.Getenv("LD_LIBRARY_PATH"); inherited != "" {
			path += string(os.PathListSeparator) + inherited
		}
		return append(env, "LD_LIBRARY_PATH="+path)
	case "darwin":
		path := dir
		if inherited := os.Getenv("DYLD_LIBRARY_PATH"); inherited != "" {
			path += string(os.PathListSeparator) + inherited
		}
		return append(env, "DYLD_LIBRARY_PATH="+path)
	}
	return env
}

package llamaruntime

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzRuntimeArchivePath(f *testing.F) {
	for _, name := range []string{"bin/llama-server", "../escape", "/tmp/escape", `..\escape`, "bin/../../escape"} {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if len(name) > 4096 {
			t.Skip()
		}
		root := filepath.Join(t.TempDir(), "runtime")
		resolved, err := securePath(root, name)
		if err != nil {
			return
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			t.Fatalf("archive path escaped extraction root: %q", name)
		}
	})
}

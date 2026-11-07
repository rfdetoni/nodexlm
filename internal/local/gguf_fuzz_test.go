package local

import (
	"os"
	"path/filepath"
	"testing"
)

// Malformed GGUF metadata is an untrusted-file boundary. Existing examples do
// not cover combinations of truncated strings/arrays and attacker-sized counts.
func FuzzInspectGGUF(f *testing.F) {
	f.Add(ggufTestFile("llama", 0, 0))
	f.Add([]byte("GGUF\x03\x00\x00\x00"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 1<<20 {
			t.Skip()
		}
		path := filepath.Join(t.TempDir(), "fuzz.gguf")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		_, _ = InspectGGUF(path)
	})
}

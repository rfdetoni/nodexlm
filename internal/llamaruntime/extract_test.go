package llamaruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// Archive extraction installs executable code: reject escaping paths, links
// and duplicate destinations before an untrusted runtime can be selected.
func TestRuntimeArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		entry     string
		kind      byte
		duplicate bool
	}{
		{"traversal", "../outside", tar.TypeReg, false}, {"absolute", "/tmp/outside", tar.TypeReg, false},
		{"backslash", `bin\outside`, tar.TypeReg, false}, {"symlink", "bin/tool", tar.TypeSymlink, false},
		{"duplicate", "bin/tool", tar.TypeReg, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw bytes.Buffer
			gz := gzip.NewWriter(&raw)
			tw := tar.NewWriter(gz)
			count := 1
			if tc.duplicate {
				count = 2
			}
			for range count {
				h := &tar.Header{Name: tc.entry, Mode: 0700, Typeflag: tc.kind}
				if tc.kind == tar.TypeReg {
					h.Size = 1
				}
				if err := tw.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if h.Size > 0 {
					_, _ = tw.Write([]byte("x"))
				}
			}
			_ = tw.Close()
			_ = gz.Close()
			if err := extractTar(bytes.NewReader(raw.Bytes()), t.TempDir()); err == nil {
				t.Fatal("unsafe runtime archive accepted")
			}
		})
	}
}

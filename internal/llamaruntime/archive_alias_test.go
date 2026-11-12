package llamaruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type runtimeTestEntry struct {
	name     string
	link     string
	kind     byte
	contents string
}

func makeRuntimeTar(t *testing.T, entries []runtimeTestEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.name, Linkname: entry.link,
			Typeflag: entry.kind, Mode: 0755,
		}
		if entry.kind == tar.TypeReg {
			header.Size = int64(len(entry.contents))
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tw.Write([]byte(entry.contents)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRuntimeTarAllowsVerifiedInArchiveSharedLibraryAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hard-link TAR aliases are used by Unix runtime releases; Windows uses ZIP")
	}
	content := "ELF library contents"
	// Preserve forward references and multiple-level aliases as used by
	// libggml.so -> libggml.so.0 -> libggml.so.0.9.6 release archives.
	archive := makeRuntimeTar(t, []runtimeTestEntry{
		{name: "bin", kind: tar.TypeDir},
		{name: "bin/libggml.so", link: "libggml.so.0", kind: tar.TypeSymlink},
		{name: "bin/libggml.so.0", link: "libggml.so.0.9.6", kind: tar.TypeSymlink},
		{name: "bin/libggml.so.0.9.6", contents: content, kind: tar.TypeReg},
		{name: "bin/libggml-hard.so", link: "bin/libggml.so.0.9.6", kind: tar.TypeLink},
		{name: "bin/llama-server", contents: "#!/bin/sh\necho ok\n", kind: tar.TypeReg},
	})
	root := t.TempDir()
	if err := extractTar(bytes.NewReader(archive), root); err != nil {
		t.Fatalf("safe SONAME links in pinned runtime were rejected: %v", err)
	}
	original, err := os.Stat(filepath.Join(root, "bin", "libggml.so.0.9.6"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"libggml.so", "libggml.so.0", "libggml-hard.so"} {
		target := filepath.Join(root, "bin", name)
		info, err := os.Lstat(target)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || !os.SameFile(original, info) {
			t.Fatalf("%s is not a hard-linked regular file from the extracted runtime", name)
		}
		contents, err := os.ReadFile(target)
		if err != nil || string(contents) != content {
			t.Fatalf("link alias %q contents mismatch: %q %v", name, contents, err)
		}
	}
	if executable(root, "llama-server") == "" {
		t.Fatal("runtime lost its executable binary")
	}
}

func TestRuntimeTarRejectsMaliciousOrBrokenAliases(t *testing.T) {
	cases := []struct {
		name    string
		entries []runtimeTestEntry
	}{
		{"absolute", []runtimeTestEntry{
			{name: "bin/libggml.so", link: "/etc/passwd", kind: tar.TypeSymlink},
		}},
		{"traversal", []runtimeTestEntry{
			{name: "bin/libggml.so", link: "../../outside", kind: tar.TypeSymlink},
		}},
		{"windows-escape", []runtimeTestEntry{
			{name: "bin/libggml.so", link: "..\\..\\outside", kind: tar.TypeSymlink},
		}},
		{"windows-drive", []runtimeTestEntry{
			{name: "bin/libggml.so", link: "C:/evil", kind: tar.TypeSymlink},
		}},
		{"dangling", []runtimeTestEntry{
			{name: "bin/libggml.so", link: "missing.so", kind: tar.TypeSymlink},
		}},
		{"cycle", []runtimeTestEntry{
			{name: "bin/libggml.so", link: "libggml.so.0", kind: tar.TypeSymlink},
			{name: "bin/libggml.so.0", link: "libggml.so", kind: tar.TypeSymlink},
		}},
		{"link-to-dir", []runtimeTestEntry{
			{name: "bin", kind: tar.TypeDir},
			{name: "bin/libggml.so", link: ".", kind: tar.TypeSymlink},
		}},
		{"duplicate-real", []runtimeTestEntry{
			{name: "bin/libggml.so", link: "libggml.so.0", kind: tar.TypeSymlink},
			{name: "bin/libggml.so.0", contents: "lib", kind: tar.TypeReg},
			{name: "bin/libggml.so", contents: "conflict", kind: tar.TypeReg},
		}},
		{"duplicate-link", []runtimeTestEntry{
			{name: "bin/libggml.so", link: "libggml.so.0", kind: tar.TypeSymlink},
			{name: "bin/libggml.so", link: "libggml.so.1", kind: tar.TypeSymlink},
		}},
		{"hardlink-traversal", []runtimeTestEntry{
			{name: "bin/libggml.so", link: "../outside", kind: tar.TypeLink},
		}},
		{"unsupported-device", []runtimeTestEntry{
			{name: "bin/raw-device", kind: tar.TypeChar},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			archive := makeRuntimeTar(t, tc.entries)
			root := t.TempDir()
			if err := extractTar(bytes.NewReader(archive), root); err == nil {
				t.Fatal("unsafe runtime archive alias was accepted")
			}
		})
	}
}

func TestRuntimeTarNeverCreatesExternalPathViaLinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "payload")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "outside")
	archive := makeRuntimeTar(t, []runtimeTestEntry{
		{name: "bin/libggml.so", link: "../../outside", kind: tar.TypeSymlink},
		{name: "bin/libggml.so.0", contents: "safe", kind: tar.TypeReg},
	})
	if err := extractTar(bytes.NewReader(archive), root); err == nil ||
		!strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("symlink traversal was not rejected: %v", err)
	}
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Fatalf("traversal created an external path: %v", err)
	}
}

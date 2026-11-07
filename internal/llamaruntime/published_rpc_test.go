// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package llamaruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func testRPCArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tarWriter := tar.NewWriter(gz)
	for _, name := range []string{"bin/llama-server", "bin/ggml-rpc-server"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		contents := []byte("#!/bin/sh\necho ok\n")
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPublishedChecksum(t *testing.T) {
	name := "rpc.tar.gz"
	sha := fmt.Sprintf("%064x", 123)
	valid := []byte(sha + "  " + name + "\n")
	actual, err := publishedChecksum(valid, name)
	if err != nil || actual != sha {
		t.Fatalf("sha %q, err %v", actual, err)
	}
	for _, invalid := range []string{"", "aabb  " + name, sha + "  " + name + "\n" + sha + "  " + name, "GG" + sha[2:] + "  " + name} {
		if _, err := publishedChecksum([]byte(invalid), name); err == nil {
			t.Fatalf("accepted bad checksum %q", invalid)
		}
	}
}

func TestPublishedRPCVerifiedInstall(t *testing.T) {
	archive := testRPCArchive(t)
	digest := sha256.Sum256(archive)
	name := fmt.Sprintf("nodexlm-rpc_%s_%s_%s_cpu.tar.gz", Version, runtime.GOOS, runtime.GOARCH)
	manifest := []byte(hex.EncodeToString(digest[:]) + "  " + name + "\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS.txt":
			w.Write(manifest)
		case "/" + name:
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if !publishedRPCSupported("cpu") {
		t.Skip("platform has no published CPU pair")
	}
	home := t.TempDir()
	srv, rpc, err := fetchPublishedRPC(context.Background(), home, "cpu", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if srv == "" || rpc == "" {
		t.Fatalf("missing binaries: %q %q", srv, rpc)
	}
}

func TestPublishedRPCTamperRejected(t *testing.T) {
	name := fmt.Sprintf("nodexlm-rpc_%s_%s_%s_cpu.tar.gz", Version, runtime.GOOS, runtime.GOARCH)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/SHA256SUMS.txt" {
			fmt.Fprintf(w, "%064x  %s\n", 1, name)
		} else {
			w.Write(testRPCArchive(t))
		}
	}))
	defer server.Close()
	if !publishedRPCSupported("cpu") {
		t.Skip("platform has no published CPU pair")
	}
	if _, _, err := fetchPublishedRPC(context.Background(), t.TempDir(), "cpu", server.URL); err == nil {
		t.Fatal("accepted corrupted runtime")
	}
}

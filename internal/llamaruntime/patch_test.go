package llamaruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCustomRuntimeMustAdvertiseUnixTransport(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	before := revision.UnixRPC
	revision.UnixRPC = true
	t.Cleanup(func() { revision.UnixRPC = before })
	if !PreferUnixRPC(context.Background(), "llama-server") {
		t.Fatal("default runtime lost its declared capability")
	}
	path := filepath.Join(t.TempDir(), "custom-llama-server")
	for _, tc := range []struct {
		help string
		want bool
	}{{"--rpc HOSTS", false}, {"--rpc unix:/PATH", true}} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+tc.help+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if got := PreferUnixRPC(context.Background(), path); got != tc.want {
			t.Fatalf("custom help %q: got %v", tc.help, got)
		}
	}
}

func TestNativeTraceAndExperimentalCUDACaches(t *testing.T) {
	t.Setenv("NODEXLM_NATIVE_TOKEN_TRACE", "")
	t.Setenv("NODEXLM_EXPERIMENTAL_CUDA", "")
	home := t.TempDir()
	normal := distributedDir(home, "cpu")
	t.Setenv("NODEXLM_NATIVE_TOKEN_TRACE", "1")
	if !nativeTraceBuild() || normal == distributedDir(home, "cpu") {
		t.Fatal("trace runtime must use its own cache")
	}
	t.Setenv("NODEXLM_EXPERIMENTAL_CUDA", "1")
	if experimentalCUDASelected("cuda") {
		t.Fatal("trace cannot use uninstrumented experimental archive")
	}
	t.Setenv("NODEXLM_NATIVE_TOKEN_TRACE", "")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		if experimentalCUDASelected("cuda") {
			t.Fatal("unsupported experimental CUDA target")
		}
		return
	}
	if !experimentalCUDASelected("cuda") || experimentalCUDASelected("cpu") {
		t.Fatal("experimental CUDA must be explicitly selected")
	}
	if digest, known := pinnedRPCDigest(experimentalCUDABaseURL, "cuda"); !known || digest != experimentalCUDADigest {
		t.Fatal("missing pinned experimental checksum")
	}
	experimental := distributedDir(home, "cuda")
	t.Setenv("NODEXLM_EXPERIMENTAL_CUDA", "")
	if experimental == distributedDir(home, "cuda") {
		t.Fatal("experimental CUDA must not overwrite normal runtime")
	}
}

func TestSmolmaxSingleRowCacheIsolation(t *testing.T) {
	t.Setenv("NODEXLM_NATIVE_TOKEN_TRACE", "")
	t.Setenv("NODEXLM_SMOLMAX_SINGLE_ROW", "")
	home := t.TempDir()
	base := distributedDir(home, "cpu")
	t.Setenv("NODEXLM_SMOLMAX_SINGLE_ROW", "1")
	want := runtime.GOOS == "linux" && runtime.GOARCH == "arm64"
	if smolmaxSingleRowBuild("cpu") != want || smolmaxSingleRowBuild("cuda") {
		t.Fatal("unexpected Smolmax build selection")
	}
	if (distributedDir(home, "cpu") != base) != want {
		t.Fatal("Smolmax cache selection mismatch")
	}
}

func TestModelWideRPCCacheIsolation(t *testing.T) {
	t.Setenv("NODEXLM_MODEL_WIDE_RPC_BUILD", "")
	base := distributedDir(t.TempDir(), "cpu")
	t.Setenv("NODEXLM_MODEL_WIDE_RPC_BUILD", "1")
	if !modelWideRPCBuild() || !strings.Contains(distributedDir(filepath.Dir(base), "cpu"), "-model-wide-v1-") {
		t.Fatal("model-wide RPC source build must not reuse published r5")
	}
}

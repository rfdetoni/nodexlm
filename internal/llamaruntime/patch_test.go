package llamaruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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

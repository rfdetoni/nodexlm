package llamaruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeEnvironmentUsesSiblingGGMLLibraries(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native library loader paths are POSIX-specific")
	}
	key := "LD_LIBRARY_PATH"
	if runtime.GOOS == "darwin" {
		key = "DYLD_LIBRARY_PATH"
	}
	t.Setenv(key, "/opt/nodexlm-existing")
	binary := filepath.Join(t.TempDir(), "bundle", "llama-server")
	want := filepath.Dir(binary)+string(os.PathListSeparator)+"/opt/nodexlm-existing"
	env := NativeEnvironment(binary)
	var got string
	for _, entry := range env {
		if strings.HasPrefix(entry,key+"=") {
			got = strings.TrimPrefix(entry,key+"=")
		}
	}
	if got != want {
		t.Fatalf("native library search must prefer sibling GGML libraries and preserve inherited path: got %q, want %q",got,want)
	}
}

func TestSupportsUnixRPCWithSiblingSharedLibraryPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux dynamic loader regression")
	}
	dir:=t.TempDir()
	binary:=filepath.Join(dir,"llama-server")
	// Simulates the same failure pattern as libggml.so.0 not being
	// discoverable: the test executable refuses to print its capabilities
	// unless the launcher prepends its own directory to LD_LIBRARY_PATH.
	script := "#!/bin/sh\n" +
        "if [ \"\${LD_LIBRARY_PATH%%:*}\" != \"" + dir + "\" ]; then\n" +
        "  echo 'error while loading shared libraries: libggml.so.0' >&2\n" +
        "  exit 127\n" +
        "fi\n" +
        "printf '%s\\n' '--rpc unix:/PATH'\n"
	if err:=os.WriteFile(binary,[]byte(script),0700);err!=nil{t.Fatal(err)}
	t.Setenv("LD_LIBRARY_PATH","/some-other-place")
	if !SupportsUnixRPC(context.Background(),binary){
		t.Fatal("Unix RPC probe incorrectly rejected an executable whose GGML libraries are in the sibling directory")
	}
	if !SupportsRPC(context.Background(),binary) {
		t.Fatal("GGML_RPC probe did not use the same runtime environment")
	}
}

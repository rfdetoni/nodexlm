package local

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeCredentialIsPrivateAndOverridesInheritedEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	t.Setenv("LLAMA_API_KEY", "inherited-do-not-use")
	dir := t.TempDir()
	model := filepath.Join(dir, "tiny.gguf")
	if err := os.WriteFile(model, ggufTestFile("llama", 0, 0), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "native-fixture")
	script := "#!/bin/sh\ndir=\x24{0%/*}\nprintf '%s\\n' \"\x24@\" > \"\x24dir/args\"\nprintf '%s' \"\x24LLAMA_API_KEY\" > \"\x24dir/key\"\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	m := &Managed{Binary: binary, ModelPath: model, Alias: "tiny.gguf"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := make(chan struct{})
	go func() {
		_ = m.Load(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
		m.Close()
	}()
	var key []byte
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		key, _ = os.ReadFile(filepath.Join(dir, "key"))
		if len(key) >= 32 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(key) < 32 || string(key) == "inherited-do-not-use" {
		t.Fatal("native process did not receive its own private credential")
	}
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), string(key)) || strings.Contains(string(args), "--api-key") {
		t.Fatal("native credential exposed in process arguments")
	}
}

func TestNativeEarlyExitReportsActionableStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	dir := t.TempDir()
	model := filepath.Join(dir, "tiny.gguf")
	if err := os.WriteFile(model, ggufTestFile("llama", 0, 0), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "exit-fixture")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\\nprintf 'RPC device disappeared\\n' >&2\\nexit 42\\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m := &Managed{Binary: binary, ModelPath: model, Alias: "tiny.gguf"}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	started := time.Now()
	err := m.Load(ctx)
	if err == nil || !strings.Contains(err.Error(), "RPC device disappeared") {
		t.Fatalf("native exit not reported: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("native crash waited for readiness timeout: %s", time.Since(started))
	}
}

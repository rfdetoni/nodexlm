package local

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A real subprocess exits after readiness, without another goroutine
// calling Wait(). Ensure must not reuse its stale HTTP endpoint.
func TestManagedInvalidatesExitedRuntime(t *testing.T) {
	if os.Getenv("NODEXLM_TEST_NATIVE_EXIT") == "1" {
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestManagedInvalidatesExitedRuntime$")
	cmd.Env = append(os.Environ(), "NODEXLM_TEST_NATIVE_EXIT=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exit := &nativeProcessExit{done: make(chan struct{})}
	go func() {
		exit.err = cmd.Wait()
		close(exit.done)
	}()
	<-exit.done
	m := &Managed{Binary: "test-managed-without-autoinstall", started: cmd, processExit: exit, endpoint: &Engine{}}
	err := m.Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "local model path required") {
		t.Fatalf("expected fresh-start validation, got %v", err)
	}
	if m.started != nil || m.processExit != nil || m.endpoint != nil {
		t.Fatal("exited native runtime remains available")
	}
	m.Close() // A reaped child must not be waited on a second time.
}

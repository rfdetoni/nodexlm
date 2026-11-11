package llamaruntime

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"time"
)

//go:embed patches/unix-rpc.patch
var unixRPCPatch []byte

func applyRPCPatch(ctx context.Context, source string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", source, "apply", "-")
	cmd.Stdin = bytes.NewReader(unixRPCPatch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("apply pinned Unix RPC patch: %w: %s", err, out)
	}
	return nil
}
func SupportsUnixRPC(ctx context.Context, binary string) bool {
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(check, binary, "--help")
	cmd.Env = NativeEnvironment(binary)
	out, _ := cmd.CombinedOutput()
	return bytes.Contains(out, []byte("unix:/")) || bytes.Contains(out, []byte("--socket PATH"))
}
func UnixRPCBundles() bool { return revision.UnixRPC }

// An explicit custom runtime must prove its own transport capability.
func PreferUnixRPC(ctx context.Context, requested string) bool {
	if requested == "" || requested == "llama-server" {
		return UnixRPCBundles()
	}
	return SupportsUnixRPC(ctx, requested)
}

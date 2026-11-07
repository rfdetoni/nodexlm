package llamaruntime

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"strings"
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
	out, _ := exec.CommandContext(check, binary, "--help").CombinedOutput()
	return bytes.Contains(out, []byte("unix:/")) || bytes.Contains(out, []byte("--socket PATH"))
}
func UnixRPCBundles() bool { return strings.Contains(revision.RPCRelease, "-r3") }

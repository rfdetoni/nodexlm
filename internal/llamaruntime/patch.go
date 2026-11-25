package llamaruntime

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

//go:embed patches/unix-rpc.patch
var unixRPCPatch []byte

//go:embed patches/parallel-rpc-loader.patch
var parallelRPCPatch []byte

//go:embed patches/rpc-command-batching.patch
var rpcBatchPatch []byte

//go:embed patches/arm32-rpc-wire.patch
var fixedRPCWirePatch []byte

//go:embed patches/native-token-activity.patch
var nativeTokenPatch []byte

//go:embed patches/smolmax-single-row.patch
var smolmaxRowPatch []byte

func nativeTraceBuild() bool { return os.Getenv("NODEXLM_NATIVE_TOKEN_TRACE") == "1" }

func smolmaxSingleRowBuild(backend string) bool {
	return runtime.GOOS == "linux" && runtime.GOARCH == "arm64" && backend == "cpu" && os.Getenv("NODEXLM_SMOLMAX_SINGLE_ROW") == "1"
}

func applyRPCPatch(ctx context.Context, source, backend string) error {
	type sourcePatch struct {
		name string
		data []byte
	}
	patches := []sourcePatch{
		{name: "Unix RPC", data: unixRPCPatch},
		{name: "fixed-width RPC wire framing", data: fixedRPCWirePatch},
		{name: "parallel tensor upload", data: parallelRPCPatch},
		{name: "bounded RPC command batching", data: rpcBatchPatch},
	}
	if nativeTraceBuild() {
		patches = append(patches, sourcePatch{name: "native token activity", data: nativeTokenPatch})
	}
	if smolmaxSingleRowBuild(backend) {
		patches = append(patches, sourcePatch{name: "Smolmax FP32 single-row", data: smolmaxRowPatch})
	}
	for _, patch := range patches {
		cmd := exec.CommandContext(ctx, "git", "-C", source, "apply", "-")
		cmd.Stdin = bytes.NewReader(patch.data)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("apply pinned %s patch: %w: %s", patch.name, err, out)
		}
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

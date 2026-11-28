package llamaruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Separate candidate; never substitute these libraries into the stock bundle.
const TrellisSourceCommit = "dc5521842f7991ceae73139f9d242d8f877e12d4"

var trellisInstallGate = make(chan struct{}, 1)

func ResolveTrellis(home, backend string) (string, error) {
	return resolveTrellis(home, backend, false)
}

func resolveTrellis(home, backend string, rpc bool) (string, error) {
	if backend == "" || backend == "auto" {
		backend = "cpu"
	}
	switch backend {
	case "cpu", "vulkan", "cuda", "metal":
	default:
		return "", errors.New("unsupported Trellis backend")
	}
	path := os.Getenv("NODEXLM_TRELLIS_BIN")
	if rpc {
		if backend != "cpu" {
			return "", errors.New("Trellis RPC candidate currently requires CPU")
		}
		path = filepath.Join(home, "runtimes", "trellis", TrellisSourceCommit, backend+"-rpc-candidate", "bin", exe("llama-server"))
		marker, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(path)), "source-commit.txt"))
		if err != nil || strings.TrimSpace(string(marker)) != TrellisSourceCommit {
			return "", errors.New("Trellis RPC candidate missing pinned revision marker")
		}
		if executable(filepath.Dir(path), "ggml-rpc-server") == "" {
			return "", errors.New("Trellis RPC worker missing")
		}
	} else if path == "" {
		path = filepath.Join(home, "runtimes", "trellis", TrellisSourceCommit, backend, "bin", exe("llama-server"))
	}
	if rpc || os.Getenv("NODEXLM_TRELLIS_BIN") == "" {
		if err := verifyTrellisInstall(filepath.Dir(filepath.Dir(path))); err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("NODEXLM_TRELLIS_BIN must be an absolute local path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Trellis runtime missing for %s; run scripts/install-trellis-runtime.sh %s on the Host or set NODEXLM_TRELLIS_BIN to a compatible local llama-server", backend, backend)
	}
	return path, nil
}

// EnsureTrellis lazily provisions the pinned source on supported Hosts with
// an installed toolchain. The stock cache is never modified.
func EnsureTrellis(ctx context.Context, home, backend string) (string, error) {
	return ensureTrellis(ctx, home, backend, false)
}

const TrellisRPCRelease = "trellis-cpu-rpc-candidate"

func EnsureTrellisRPC(ctx context.Context, home string) (string, string, error) {
	server, err := ensureTrellis(ctx, home, "cpu", true)
	if err != nil {
		return "", "", err
	}
	worker := executable(filepath.Dir(server), "ggml-rpc-server")
	if worker == "" || !SupportsRPC(ctx, server) {
		return "", "", errors.New("Trellis RPC candidate failed runtime capability check")
	}
	return server, worker, nil
}

func ensureTrellis(ctx context.Context, home, backend string, rpc bool) (string, error) {
	if binary, err := resolveTrellis(home, backend, rpc); err == nil {
		return binary, nil
	}
	if !rpc && os.Getenv("NODEXLM_TRELLIS_BIN") != "" {
		return ResolveTrellis(home, backend)
	}
	if backend == "" || backend == "auto" {
		backend = "cpu"
	}
	switch backend {
	case "cpu", "vulkan", "cuda", "metal":
	default:
		return "", errors.New("unsupported Trellis backend")
	}
	select {
	case trellisInstallGate <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-trellisInstallGate }()
	if binary, err := resolveTrellis(home, backend, rpc); err == nil {
		return binary, nil
	}
	root := filepath.Join(home, "runtimes", "trellis", TrellisSourceCommit)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	cacheName := backend
	if rpc {
		cacheName += "-rpc-candidate"
	}
	dest := filepath.Join(root, cacheName)
	if _, err := os.Lstat(dest); err == nil {
		// A corrupt or legacy unsealed cache must never be executed or
		// deleted silently. Retain the old files for operator inspection.
		quarantine, err := os.MkdirTemp(root, ".invalid-"+cacheName+"-")
		if err != nil {
			return "", err
		}
		if err := os.Rename(dest, filepath.Join(quarantine, "runtime")); err != nil {
			_ = os.Remove(quarantine)
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	stage, err := os.MkdirTemp(root, ".build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	// Keep full compiler diagnostics on disk, not in memory. Retain a small
	// actionable error on failure and avoid leaking any local credentials.
	log, err := os.CreateTemp(root, backend+"-build-*.log")
	if err != nil {
		return "", err
	}
	defer log.Close()
	run := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Trellis %s failed: %w (build log: %s)", name, err, log.Name())
		}
		return nil
	}
	src, build := filepath.Join(stage, "src"), filepath.Join(stage, "build")
	if err := run("git", "init", "-q", src); err != nil {
		return "", err
	}
	if err := run("git", "-C", src, "fetch", "--depth=1", "https://github.com/halo-box/strix-llama.cpp.git", TrellisSourceCommit); err != nil {
		return "", err
	}
	if err := run("git", "-C", src, "checkout", "-q", "--detach", "FETCH_HEAD"); err != nil {
		return "", err
	}
	revision, err := exec.CommandContext(ctx, "git", "-C", src, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(revision)) != TrellisSourceCommit {
		return "", errors.New("Trellis source revision mismatch")
	}
	rpcFlag := "OFF"
	if rpc {
		rpcFlag = "ON"
	}
	flags := []string{"-S", src, "-B", build, "-DCMAKE_BUILD_TYPE=Release", "-DGGML_RPC=" + rpcFlag, "-DGGML_BACKEND_DL=OFF", "-DGGML_NATIVE=ON", "-DGGML_OPENMP=OFF", "-DBUILD_SHARED_LIBS=OFF", "-DLLAMA_CURL=OFF", "-DLLAMA_BUILD_TESTS=OFF", "-DLLAMA_BUILD_EXAMPLES=OFF", "-DLLAMA_BUILD_TOOLS=ON", "-DLLAMA_BUILD_SERVER=ON", "-DLLAMA_BUILD_UI=OFF", "-DLLAMA_USE_PREBUILT_UI=OFF"}
	for _, name := range []string{"vulkan", "cuda", "metal"} {
		value := "OFF"
		if backend == name {
			value = "ON"
		}
		flags = append(flags, "-DGGML_"+strings.ToUpper(name)+"="+value)
	}
	if err := run("cmake", flags...); err != nil {
		return "", err
	}
	targets := []string{"--build", build, "--config", "Release", "--parallel", "2", "--target", "llama-server"}
	if rpc {
		targets = append(targets, "ggml-rpc-server")
	}
	if err := run("cmake", targets...); err != nil {
		return "", err
	}
	server := executable(filepath.Join(build, "bin"), "llama-server")
	if server == "" {
		return "", errors.New("Trellis build did not produce llama-server")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	check := exec.CommandContext(checkCtx, server, "--help")
	check.Env = NativeEnvironment(server)
	help, err := check.CombinedOutput()
	if err != nil || !strings.Contains(string(help), "--ple") {
		return "", errors.New("Trellis binary does not advertise external PLE support")
	}
	install := filepath.Join(stage, "install")
	if err := os.MkdirAll(filepath.Join(install, "bin"), 0700); err != nil {
		return "", err
	}
	// Static builds normally contain only the executable. Keep sibling DLLs
	// and dynamic libraries when the platform/toolchain emits them.
	if rpc && executable(filepath.Dir(server), "ggml-rpc-server") == "" {
		return "", errors.New("Trellis build did not produce RPC worker")
	}
	entries, err := os.ReadDir(filepath.Dir(server))
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (name != filepath.Base(server) && (!rpc || name != exe("ggml-rpc-server")) && !strings.HasSuffix(name, ".dll") && !strings.HasSuffix(name, ".dylib") && !strings.Contains(name, ".so")) {
			continue
		}
		if err := copyTrellisFile(filepath.Join(filepath.Dir(server), name), filepath.Join(install, "bin", name)); err != nil {
			return "", err
		}
	}
	if err := copyTrellisFile(filepath.Join(src, "LICENSE"), filepath.Join(install, "LICENSE")); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(install, "source-commit.txt"), []byte(TrellisSourceCommit+"\n"), 0600); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := sealTrellisInstall(install); err != nil {
		return "", err
	}
	if err := os.Rename(install, dest); err != nil {
		if binary, resolved := resolveTrellis(home, backend, rpc); resolved == nil {
			return binary, nil
		}
		return "", err
	}
	return resolveTrellis(home, backend, rpc)
}

func copyTrellisFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

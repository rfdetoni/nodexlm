// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package llamaruntime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// A reviewed, immutable upstream release and per-artifact checksums.
// Update the tag and all hashes together, never download unverified "latest".
var repoURL = "https://github.com/ggml-org/llama.cpp/releases/download/" + Version + "/"

type Artifact struct{ Name, SHA256 string }

var releases = map[string]Artifact{
	"linux/amd64/cpu":      {"llama-b12143-bin-ubuntu-x64.tar.gz", "23776b0ecc71afaa69f0827c78a0aa07ba2d9e2a71e205e4633d16729c5fef94"},
	"linux/arm64/cpu":      {"llama-b12143-bin-ubuntu-arm64.tar.gz", "caf94c5ba28ff3a16f89e27e01223675bbd98a74559c557f5004967314f1ab04"},
	"linux/amd64/vulkan":   {"llama-b12143-bin-ubuntu-vulkan-x64.tar.gz", "dd983d8eaa85e1e71fba4458441b6a507f86bb50db6b5675d1e6aa788ecccbfc"},
	"linux/arm64/vulkan":   {"llama-b12143-bin-ubuntu-vulkan-arm64.tar.gz", "ca8ed0e09076a4c5e6ea2998a1c49af9abe691b008db4a297cb92f2e816ab38f"},
	"windows/amd64/cpu":    {"llama-b12143-bin-win-cpu-x64.zip", "41ff350f7f4cef938ecaa4cb69e91cd79adfe76d09652f4bd7e325988dad7b99"},
	"windows/arm64/cpu":    {"llama-b12143-bin-win-cpu-arm64.zip", "354ccaf0856539cea0c0d140721771df8dc0c219096472ea9c8e7d4f957704dc"},
	"windows/amd64/vulkan": {"llama-b12143-bin-win-vulkan-x64.zip", "293ab85762b3d2722fc55d6e6db906546971c2b4802de84ec5ca542397314340"},
	"windows/arm64/vulkan": {"llama-b12143-bin-win-vulkan-arm64.zip", "946223d015b69d7c9f6960a051daf782745378eca0a068d552fe70bf31d72b76"},
	"darwin/amd64/metal":   {"llama-b12143-bin-macos-x64.tar.gz", "eaf1cd682da34b289f00e4ee5d4c413ca5b9709372fe21bb492cbfba7ae70f01"},
	"darwin/arm64/metal":   {"llama-b12143-bin-macos-arm64.tar.gz", "83dd79b6d4caf3106a30d48a1237f3a391af772e94d796ba9c3322076cfea851"},
}

func Backend(requested string) (string, error) {
	if requested == "" || requested == "auto" {
		if runtime.GOOS == "darwin" {
			return "metal", nil
		}
		// Prefer the portable CPU build unless the operator deliberately enables a
		// GPU backend. A visible GPU does not establish that drivers work.
		return "cpu", nil
	}
	if requested != "cpu" && requested != "vulkan" && requested != "metal" {
		return "", fmt.Errorf("backend %q unsupported; use auto, cpu, vulkan or metal", requested)
	}
	return requested, nil
}
func installDir(home, backend string) string {
	return filepath.Join(home, "runtimes", "llama.cpp", Version, runtime.GOOS+"-"+runtime.GOARCH, backend)
}
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
func executable(dir, name string) string {
	var found string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == exe(name) {
			found = p
			return io.EOF
		}
		return nil
	})
	return found
}
func Installed(home, backend string) (string, string, error) {
	b, err := Backend(backend)
	if err != nil {
		return "", "", err
	}
	dir := installDir(home, b)
	server := executable(dir, "llama-server")
	if server == "" {
		return "", "", os.ErrNotExist
	}
	rpc := executable(dir, "ggml-rpc-server")
	if rpc == "" {
		rpc = executable(dir, "rpc-server")
	}
	return server, rpc, nil
}
func Ensure(ctx context.Context, home, backend string) (string, string, error) {
	b, err := Backend(backend)
	if err != nil {
		return "", "", err
	}
	if server, rpc, err := Installed(home, b); err == nil {
		return server, rpc, nil
	}
	a, ok := releases[runtime.GOOS+"/"+runtime.GOARCH+"/"+b]
	if !ok {
		return "", "", fmt.Errorf("no verified official llama.cpp binary for %s/%s backend %s; use your own compatible build", runtime.GOOS, runtime.GOARCH, b)
	}
	dir := installDir(home, b)
	if err = os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return "", "", err
	}
	// Atomic directory installation: concurrent installers never overwrite a live runtime.
	stage, err := os.MkdirTemp(filepath.Dir(dir), ".llama-install-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(stage)
	tmp, err := os.CreateTemp(stage, "download-")
	if err != nil {
		return "", "", err
	}
	defer tmp.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repoURL+a.Name, nil)
	if err != nil {
		return "", "", err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("official llama.cpp download HTTP %d", resp.StatusCode)
	}
	const maxArchive = int64(800 << 20)
	if resp.ContentLength > maxArchive {
		return "", "", errors.New("llama.cpp archive exceeds limit")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, maxArchive+1))
	if err != nil {
		return "", "", err
	}
	if n > maxArchive {
		return "", "", errors.New("llama.cpp archive exceeds limit")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), a.SHA256) {
		return "", "", errors.New("official llama.cpp archive SHA-256 mismatch")
	}
	if _, err = tmp.Seek(0, 0); err != nil {
		return "", "", err
	}
	payload := filepath.Join(stage, "payload")
	if err = os.Mkdir(payload, 0700); err != nil {
		return "", "", err
	}
	if strings.HasSuffix(a.Name, ".zip") {
		err = extractZip(tmp, payload)
	} else {
		err = extractTar(tmp, payload)
	}
	if err != nil {
		return "", "", err
	}
	if executable(payload, "llama-server") == "" {
		return "", "", errors.New("downloaded artifact has no llama-server binary")
	}
	if err = os.Rename(payload, dir); err != nil {
		if srv, rpc, existingErr := Installed(home, b); existingErr == nil {
			return srv, rpc, nil
		}
		return "", "", err
	}
	return Installed(home, b)
}
func securePath(root, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || strings.Contains(name, "\\") {
		return "", errors.New("unsafe runtime archive path")
	}
	dest := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("archive path escapes installation")
	}
	return dest, nil
}
func unpackFile(dst string, src io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	written, err := io.Copy(f, io.LimitReader(src, 1<<30))
	if err == nil && written >= 1<<30 {
		err = errors.New("archive file exceeds limit")
	}
	if er := f.Close(); err == nil {
		err = er
	}
	if err != nil {
		return err
	}
	if mode&0111 != 0 {
		return os.Chmod(dst, 0700)
	}
	return nil
}
func extractTar(r io.Reader, root string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	t := tar.NewReader(gz)
	var total int64
	for i := 0; i < 4096; i++ {
		h, err := t.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		path, err := securePath(root, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(path, 0700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if h.Size < 0 || h.Size > 1<<30 {
				return errors.New("oversized archive entry")
			}
			total += h.Size
			if total > 2<<30 {
				return errors.New("runtime archive expanded size limit exceeded")
			}
			if err = unpackFile(path, t, os.FileMode(h.Mode)); err != nil {
				return err
			}
		default:
			return errors.New("runtime archive contains symlink or unsupported entry")
		}
	}
	return errors.New("runtime archive has too many files")
}
func extractZip(f *os.File, root string) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	z, err := zip.NewReader(f, info.Size())
	if err != nil {
		return err
	}
	if len(z.File) > 4096 {
		return errors.New("runtime archive has too many files")
	}
	var total uint64
	for _, item := range z.File {
		dst, err := securePath(root, item.Name)
		if err != nil {
			return err
		}
		if item.Mode()&os.ModeSymlink != 0 {
			return errors.New("runtime archive contains symlink")
		}
		if item.FileInfo().IsDir() {
			if err = os.MkdirAll(dst, 0700); err != nil {
				return err
			}
			continue
		}
		total += item.UncompressedSize64
		if total > 2<<30 {
			return errors.New("runtime archive expanded size limit exceeded")
		}
		rd, err := item.Open()
		if err != nil {
			return err
		}
		err = unpackFile(dst, rd, item.Mode())
		rd.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
func AutoExecutable(ctx context.Context, home, requested string) (string, error) {
	if requested != "" && requested != "llama-server" {
		return requested, nil
	}
	if p, err := exec.LookPath("llama-server"); err == nil {
		return p, nil
	}
	s, _, err := Ensure(ctx, home, "auto")
	return s, err
}

// Official release archives are preferred. When they were built without RPC,
// create a matched llama-server + ggml-rpc-server pair from the pinned upstream
// source. Both programs must use the same RPC protocol revision.

func SupportsRPC(ctx context.Context, server string) bool {
	// The precompiled release contains libggml.so.N beside the executable,
	// not in the system library path. Match the runtime environment used by
	// the Guest process, or --help will fail before checking RPC support.
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(testCtx, server, "--help")
	if runtime.GOOS == "linux" {
		p := filepath.Dir(server)
		if prev := os.Getenv("LD_LIBRARY_PATH"); prev != "" {
			p += string(os.PathListSeparator) + prev
		}
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+p)
	}
	out, err := cmd.CombinedOutput()
	// llama-server commonly prints help to stderr and exits 0 or 1.
	return (err == nil || len(out) > 0) && strings.Contains(string(out), "--rpc")
}

// Use a fresh cache for the corrected Linux x64 runtime package. Existing
// b12143 cpu-rpc directories may contain binaries with missing GGML SONAME
// libraries and must not be reused after upgrading the CLI.
func distributedDir(home, backend string) string {
	dir := installDir(home, backend) + "-rpc"
	if publishedRPCSupported(backend) {
		return dir + "-" + revision.RPCRelease
	}
	return dir
}

func installedDistributed(home, backend string) (string, string, error) {
	dir := distributedDir(home, backend)
	server := executable(dir, "llama-server")
	rpc := executable(dir, "ggml-rpc-server")
	if rpc == "" {
		rpc = executable(dir, "rpc-server")
	}
	if server == "" || rpc == "" {
		return "", "", os.ErrNotExist
	}
	return server, rpc, nil
}
func EnsureDistributed(ctx context.Context, home, requested string) (string, string, error) {
	backend, err := Backend(requested)
	if err != nil {
		return "", "", err
	}
	if server, rpc, err := installedDistributed(home, backend); err == nil {
		return server, rpc, nil
	}
	// Prefer a ready-to-run, pinned RPC pair: ordinary Linux Guests should not
	// need CMake, a compiler or root access just to join a trusted cluster.
	var publishedErr error
	if publishedRPCSupported(backend) {
		fmt.Fprintln(os.Stderr, "NodexLM: preparando runtime RPC Linux x64 verificado...")
		var s, p string
		s, p, publishedErr = fetchPublishedRPC(ctx, home, backend, publishedRPCBaseURL)
		if publishedErr == nil && SupportsRPC(ctx, s) {
			return s, p, nil
		}
		if publishedErr == nil {
			publishedErr = errors.New("downloaded runtime lacks RPC support")
		}
	}
	// Existing official archives may already include matching RPC binaries.
	server, rpc, err := Ensure(ctx, home, backend)
	if err == nil && rpc != "" && SupportsRPC(ctx, server) {
		return server, rpc, nil
	}
	// Some official releases omit GGML_RPC. Automatically build the missing
	// backend from exactly the same signed release source for both executables.
	if _, err := exec.LookPath("cmake"); err != nil {
		if publishedErr != nil {
			return "", "", fmt.Errorf("precompiled RPC download failed: %v; local fallback requires git, CMake and a C/C++ compiler (Ubuntu/Debian: sudo apt install -y git cmake build-essential)", publishedErr)
		}
		return "", "", errors.New("RPC runtime requires CMake and a C/C++ compiler to build the pinned llama.cpp release (Ubuntu/Debian: sudo apt install -y git cmake build-essential)")
	}
	if _, err := exec.LookPath("c++"); err != nil {
		return "", "", errors.New("RPC source build requires a C++ compiler (Ubuntu/Debian: sudo apt install -y build-essential)")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", "", errors.New("RPC runtime needs git to retrieve pinned llama.cpp source")
	}
	dest := distributedDir(home, backend)
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return "", "", err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".build-rpc-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(stage)
	src := filepath.Join(stage, "src")
	fetch := exec.CommandContext(ctx, "git", "clone", "--quiet", "--depth=1", "--branch", Version, "https://github.com/ggml-org/llama.cpp", src)
	fetch.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := fetch.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("fetch upstream llama.cpp: %w: %s", err, string(out))
	}
	rev := exec.CommandContext(ctx, "git", "-C", src, "rev-parse", "HEAD")
	raw, err := rev.Output()
	if err != nil || strings.TrimSpace(string(raw)) != SourceCommit {
		return "", "", errors.New("pinned llama.cpp source commit mismatch")
	}
	if err := applyRPCPatch(ctx, src); err != nil {
		return "", "", err
	}
	build := filepath.Join(stage, "build")
	flags := []string{"-S", src, "-B", build, "-DGGML_RPC=ON", "-DLLAMA_BUILD_TESTS=OFF", "-DLLAMA_BUILD_EXAMPLES=ON", "-DLLAMA_BUILD_SERVER=ON", "-DCMAKE_BUILD_TYPE=Release"}
	switch backend {
	case "vulkan":
		flags = append(flags, "-DGGML_VULKAN=ON")
	case "metal":
		flags = append(flags, "-DGGML_METAL=ON")
	}
	configure := exec.CommandContext(ctx, "cmake", flags...)
	if out, err := configure.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("configure llama.cpp with RPC: %w: %s", err, string(out))
	}
	compile := exec.CommandContext(ctx, "cmake", "--build", build, "--config", "Release", "--parallel", "2")
	if out, err := compile.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("build llama.cpp with RPC: %w: %s", err, string(out))
	}
	// Keep the complete bin directory so sibling ggml dynamic libraries remain
	// alongside the executables on Linux, Windows and macOS.
	bin := filepath.Join(build, "bin")
	if executable(bin, "llama-server") == "" || (executable(bin, "ggml-rpc-server") == "" && executable(bin, "rpc-server") == "") {
		return "", "", errors.New("upstream RPC build did not produce both required programs")
	}
	if err := os.Rename(bin, dest); err != nil {
		if s, p, e := installedDistributed(home, backend); e == nil {
			return s, p, nil
		}
		return "", "", err
	}
	return installedDistributed(home, backend)
}

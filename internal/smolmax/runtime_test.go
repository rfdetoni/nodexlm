package smolmax

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func createRuntime(t *testing.T, features []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llama-server")
	if err := os.WriteFile(path, []byte("test executable"), 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("test executable"))
	raw, _ := json.Marshal(manifest{SHA256: hex.EncodeToString(digest[:]), Features: features})
	if err := os.WriteFile(path+".smolmax.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOptInAndRuntimeValidation(t *testing.T) {
	binary := createRuntime(t, []string{"moe_pager", "weight_streaming"})
	if env, err := NativeEnvironment(binary, "", false, 0, "auto", 8); err != nil || len(env) != 0 {
		t.Fatalf("disabled mode must be a no-op: %v %v", env, err)
	}
	if _, err := NativeEnvironment(binary, "moe", true, 128, "0", 1); err == nil {
		t.Fatal("accepted paging over RPC")
	}
	if _, err := NativeEnvironment(binary, "moe", true, 128, "auto", 0); err == nil {
		t.Fatal("accepted GPU paging")
	}
	if _, err := NativeEnvironment(binary, "moe", false, 0, "0", 0); err == nil {
		t.Fatal("accepted dense model")
	}
	if _, err := NativeEnvironment(binary, "invalid", true, 128, "0", 0); err == nil {
		t.Fatal("accepted invalid mode")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		return
	}
	t.Setenv("NODEXLM_SMOLMAX_EXPERT_POOL", "32")
	t.Setenv("NODEXLM_SMOLMAX_RESIDENT_MB", "512")
	env, err := NativeEnvironment(binary, "moe", true, 128, "0", 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, " ")
	for _, name := range []string{"GGML_MOE_PAGER=1", "GGML_MOE_POOL=32", "GGML_MOE_READERS=2"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("missing %q in %s", name, joined)
		}
	}
	if strings.Contains(joined, "GGML_MOE_BIAS") {
		t.Fatal("routing bias must not be set by default")
	}
	env, err = NativeEnvironment(binary, "stream", false, 0, "0", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(env, " "), "LLAMA_STREAM_RESIDENT_MB=512") {
		t.Fatal(env)
	}
	if err := os.WriteFile(binary, []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NativeEnvironment(binary, "moe", true, 128, "0", 0); err == nil {
		t.Fatal("accepted hash mismatch")
	}
}

func TestManifestAndBounds(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("Linux-specific native paging")
	}
	binary := createRuntime(t, []string{"moe_pager"})
	if _, err := NativeEnvironment(binary, "stream", false, 0, "0", 0); err == nil {
		t.Fatal("accepted unregistered feature")
	}
	t.Setenv("NODEXLM_SMOLMAX_EXPERT_POOL", "999")
	if _, err := NativeEnvironment(binary, "moe", true, 16, "0", 0); err == nil {
		t.Fatal("accepted too many experts")
	}
	t.Setenv("NODEXLM_SMOLMAX_EXPERT_POOL", "")
	if _, err := NativeEnvironment(binary, "moe", true, 16, "0", 0); err == nil {
		t.Fatal("accepted implicit expert pool without a memory budget")
	}
	t.Setenv("NODEXLM_SMOLMAX_EXPERT_POOL", "4")
	t.Setenv("NODEXLM_SMOLMAX_VERIFY", "1")
	env, err := NativeEnvironment(binary, "moe", true, 16, "0", 0)
	if err != nil || !strings.Contains(strings.Join(env, " "), "GGML_MOE_VERIFY=1") {
		t.Fatalf("%v %v", env, err)
	}
}

func TestProfilingAndRepackCacheAreExplicit(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("Linux/Android only")
	}
	binary := createRuntime(t, []string{"moe_pager"})
	t.Setenv("NODEXLM_SMOLMAX_EXPERT_POOL", "8")
	t.Setenv("NODEXLM_SMOLMAX_OPPROF", "2")
	t.Setenv("NODEXLM_SMOLMAX_REPACK_CACHE", "relative.cache")
	if _, err := NativeEnvironment(binary, "moe", true, 32, "0", 0); err == nil {
		t.Fatal("accepted relative repack cache path")
	}
	cache := filepath.Join(t.TempDir(), "weights.cache")
	t.Setenv("NODEXLM_SMOLMAX_REPACK_CACHE", cache)
	env, err := NativeEnvironment(binary, "moe", true, 32, "0", 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "GGML_OPPROF=2") || !strings.Contains(joined, "GGML_REPACK_CACHE="+cache) {
		t.Fatalf("missing explicit profiler/cache settings: %s", joined)
	}
}

func TestCapabilitiesAndRepackCacheBinding(t *testing.T) {
	binary := createRuntime(t, []string{"moe_pager", "repack_cache"})
	caps, err := InspectCapabilities(binary)
	if err != nil || !caps.CPUKernels || !caps.MoEPager || !caps.RepackCache || caps.RPC || caps.Vulkan {
		t.Fatalf("unexpected capabilities: %+v err=%v", caps, err)
	}

	cachePath := filepath.Join(t.TempDir(), "repack.cache")
	binding := RepackCacheBinding{
		GGUFSHA256:    strings.Repeat("a", 64),
		Architecture:  "qwen3moe",
		Quant:         "Q4_K_M",
		RuntimeSHA256: strings.Repeat("b", 64),
		Platform:      "linux/arm64",
		Mode:          "moe",
		MaxBytes:      1024,
	}
	if err := ValidateRepackCache(cachePath, binding); err != nil {
		t.Fatalf("initial repack binding failed: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("packed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepackCache(cachePath, binding); err != nil {
		t.Fatalf("matching repack binding rejected: %v", err)
	}
	otherModel := binding
	otherModel.GGUFSHA256 = strings.Repeat("c", 64)
	if err := ValidateRepackCache(cachePath, otherModel); err == nil {
		t.Fatal("accepted repack cache across different GGUF SHA-256")
	}
	overBudget := binding
	overBudget.MaxBytes = 2
	if err := ValidateRepackCache(cachePath, overBudget); err == nil {
		t.Fatal("accepted repack cache exceeding disk budget")
	}
}

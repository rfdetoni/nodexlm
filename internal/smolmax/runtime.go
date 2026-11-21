// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
// Package smolmax integrates opt-in, locally built smolmaxxing native runtimes.
// It never claims that the stock NodexLM llama.cpp contains the TV-box patch.
package smolmax

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type manifest struct {
	SHA256   string   `json:"sha256"`
	Features []string `json:"features"`
}

func checkRuntime(binary, feature string) error {
	f, err := os.Open(binary + ".smolmax.json")
	if err != nil {
		return fmt.Errorf("smolmax runtime manifest missing: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return err
	}
	if len(raw) > 4096 {
		return errors.New("smolmax manifest too large")
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("invalid smolmax manifest: %w", err)
	}
	hash, err := hex.DecodeString(m.SHA256)
	if err != nil || len(hash) != sha256.Size {
		return errors.New("smolmax manifest requires a SHA-256 digest")
	}
	found := false
	for _, f := range m.Features {
		if f == feature {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("smolmax binary was not registered for %s", feature)
	}
	in, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer in.Close()
	h := sha256.New()
	if _, err := io.Copy(h, in); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), m.SHA256) {
		return errors.New("smolmax native binary changed since its manifest was created")
	}
	return nil
}

func intEnv(key string, fallback, min, max int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return n, nil
}

// NativeEnvironment configures only known smolmaxxing environment switches.
// A per-binary SHA-256 manifest prevents accidentally enabling experimental
// flags on a different or upgraded llama-server. The manifest is a LOCAL
// operator attestation, not proof of kernel correctness or a signed release.
// mode=off is a strict no-op. Paging/streaming is currently CPU-only and may
// not be combined with RPC placement: neither path pages remote expert tensors.
func NativeEnvironment(binary, mode string, moe bool, experts uint32, gpu string, guests int) ([]string, error) {
	if mode == "" || mode == "off" {
		return nil, nil
	}
	if mode != "moe" && mode != "stream" {
		return nil, errors.New("NODEXLM_SMOLMAX_MODE must be off, moe or stream")
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		return nil, errors.New("smolmax experimental native paging requires Linux/Android")
	}
	if guests != 0 {
		return nil, errors.New("smolmax paging cannot currently be combined with distributed RPC")
	}
	if gpu != "0" {
		return nil, errors.New("smolmax experimental paging requires GPU layers set to 0")
	}
	if mode == "moe" && (!moe || experts == 0) {
		return nil, errors.New("smolmax MoE paging requires GGUF expert metadata")
	}
	feature := map[string]string{"moe": "moe_pager", "stream": "weight_streaming"}[mode]
	if err := checkRuntime(binary, feature); err != nil {
		return nil, err
	}
	readers, err := intEnv("NODEXLM_SMOLMAX_READERS", 2, 1, 16)
	if err != nil {
		return nil, err
	}
	verify := os.Getenv("NODEXLM_SMOLMAX_VERIFY")
	if verify != "" && verify != "0" && verify != "1" {
		return nil, errors.New("NODEXLM_SMOLMAX_VERIFY must be 0 or 1")
	}
	prof, err := intEnv("NODEXLM_SMOLMAX_OPPROF", 0, 0, 2)
	if err != nil {
		return nil, err
	}
	extra := make([]string, 0, 3)
	if prof > 0 {
		extra = append(extra, "GGML_OPPROF="+strconv.Itoa(prof))
	}
	if cache := os.Getenv("NODEXLM_SMOLMAX_REPACK_CACHE"); cache != "" {
		if !filepath.IsAbs(cache) {
			return nil, errors.New("NODEXLM_SMOLMAX_REPACK_CACHE must be an absolute file path")
		}
		if info, err := os.Lstat(cache); err == nil {
			if !info.Mode().IsRegular() {
				return nil, errors.New("smolmax repack cache must be a regular file, not a directory or symlink")
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if parent, err := os.Stat(filepath.Dir(cache)); err != nil || !parent.IsDir() {
			return nil, errors.New("smolmax repack cache directory does not exist")
		}
		extra = append(extra, "GGML_REPACK_CACHE="+cache)
	}
	if mode == "moe" {
		if os.Getenv("NODEXLM_SMOLMAX_EXPERT_POOL") == "" {
			return nil, errors.New("NODEXLM_SMOLMAX_EXPERT_POOL is required: choose a value measured to fit the available RAM")
		}
		pool, err := intEnv("NODEXLM_SMOLMAX_EXPERT_POOL", 0, 1, int(experts))
		if err != nil {
			return nil, err
		}
		prefetch, err := intEnv("NODEXLM_SMOLMAX_PREFETCH_K", 0, 0, 16)
		if err != nil {
			return nil, err
		}
		env := []string{
			"GGML_MOE_PAGER=1",
			"GGML_MOE_POOL=" + strconv.Itoa(pool),
			"GGML_MOE_READERS=" + strconv.Itoa(readers),
			"GGML_MOE_PREFETCH_K=" + strconv.Itoa(prefetch),
			"GGML_MOE_STATS=1",
		}
		if verify == "1" {
			env = append(env, "GGML_MOE_VERIFY=1")
		}
		// Do NOT bias routing, prune vocabulary, skip experts or change
		// the number of active experts: those change model output quality.
		return append(env, extra...), nil
	}
	if os.Getenv("NODEXLM_SMOLMAX_RESIDENT_MB") == "" {
		return nil, errors.New("NODEXLM_SMOLMAX_RESIDENT_MB is required: reserve enough RAM for buffers and KV cache")
	}
	resident, err := intEnv("NODEXLM_SMOLMAX_RESIDENT_MB", 0, 64, 65536)
	if err != nil {
		return nil, err
	}
	window, err := intEnv("NODEXLM_SMOLMAX_WINDOW_MB", 128, 8, 4096)
	if err != nil {
		return nil, err
	}
	env := []string{
		"LLAMA_STREAM_RESIDENT_MB=" + strconv.Itoa(resident),
		"GGML_STREAM_WINDOW_MB=" + strconv.Itoa(window),
		"GGML_STREAM_READERS=" + strconv.Itoa(readers),
		"GGML_STREAM_STATS=1",
	}
	if verify == "1" {
		env = append(env, "GGML_STREAM_VERIFY=1")
	}
	return append(env, extra...), nil
}

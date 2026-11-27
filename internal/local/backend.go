// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/config"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

const (
	BackendLlama     = "llama.cpp"
	BackendMistralRS = "mistralrs"
)

func EffectiveBackend(name string) string {
	if name == "" {
		return BackendLlama
	}
	return name
}

type backendLaunchSpec struct {
	Binary    string
	Args      []string
	Env       []string
	APIKey    string
	ReadyPath string
	Timeout   time.Duration
}

// These are process adapters, not an interchangeable distributed tensor
// protocol. The existing authenticated NodeXLM API stays in front of either.
func (m *Managed) backendLaunch(port, nctx, threads int, gpu string, meta GGUFInfo) (backendLaunchSpec, error) {
	switch EffectiveBackend(m.Backend) {
	case BackendLlama:
		key, err := config.RandomSecret()
		if err != nil {
			return backendLaunchSpec{}, fmt.Errorf("generate inference credential: %w", err)
		}
		args := m.args(port, nctx, threads, gpu, meta.BlockCount)
		if m.TrellisRuntime {
			if len(m.RPCServers) > 0 {
				return backendLaunchSpec{}, errors.New("Trellis RPC is not validated; use Host-only")
			}
			if m.SpeculativeMode != "" && m.SpeculativeMode != "off" {
				return backendLaunchSpec{}, errors.New("Trellis adapter requires speculation off until validated")
			}
			if m.ParallelSlots > 1 || m.MoECacheMiB != 0 || m.MoECacheCPUShare != 0 {
				return backendLaunchSpec{}, errors.New("Trellis adapter requires one slot and no NodexLM-specific MoE cache")
			}
			ple, err := TrellisPLE(m.ModelPath, meta)
			if err != nil {
				return backendLaunchSpec{}, err
			}
			if ple != "" {
				args = append(args, "--ple", ple)
				// The sidecar is tens of GiB; keep lookup rows on disk.
				if m.LazyMode == "" || m.LazyMode == "auto" {
					args = append(args, "--lazy-mode", "on")
				}
			}
		}
		mode := os.Getenv("NODEXLM_SMOLMAX_MODE")
		if mode == "moe" || mode == "stream" {
			if m.VisionProjectorPath != "" {
				return backendLaunchSpec{}, errors.New("Smolmax paging and streaming do not support vision projectors")
			}
			if os.Getpagesize() != 4096 {
				return backendLaunchSpec{}, errors.New("Smolmax paging and streaming require 4 KiB OS pages")
			}
			if m.ParallelSlots > 1 || (m.SpeculativeMode != "" && m.SpeculativeMode != "off") || m.DraftModelPath != "" {
				return backendLaunchSpec{}, errors.New("Smolmax paging and streaming currently require one slot without speculative decoding")
			}
			if m.LoadMode != "" && m.LoadMode != "auto" {
				return backendLaunchSpec{}, errors.New("Smolmax paging and streaming require the default mapped model load mode")
			}
			if os.Getenv("NODEXLM_SMOLMAX_REPACK_CACHE") == "" {
				// The pager and stream hooks require quantized tensors still on the model mapping.
				args = append(args, "--no-repack")
			}
		}
		if m.VisionProjectorPath != "" {
			if err := ValidateVisionProjector(m.VisionProjectorPath, m.ModelPath); err != nil {
				return backendLaunchSpec{}, err
			}
			// Keep projector weights on the Host CPU, avoiding unplanned GPU allocation.
			args = append(args, "--mmproj", m.VisionProjectorPath, "--no-mmproj-offload")
		}
		if meta.PoolingType >= 1 && meta.PoolingType <= 4 {
			args = append(args, "--embedding")
		}
		timeout := 90 * time.Second
		if len(m.RPCServers) > 0 || m.TrellisRuntime {
			timeout = 75 * time.Minute
		}
		return backendLaunchSpec{Binary: m.Binary, Args: args, Env: []string{"LLAMA_API_KEY=" + key},
			APIKey: key, ReadyPath: "/health", Timeout: timeout}, nil
	case BackendMistralRS:
		if m.VisionProjectorPath != "" {
			return backendLaunchSpec{}, errors.New("vision projector is supported only by llama.cpp")
		}
		if os.Getenv("NODEXLM_EXPERIMENTAL_MISTRALRS") != "1" {
			return backendLaunchSpec{}, errors.New("mistral.rs is experimental and its local HTTP server has no authentication; set NODEXLM_EXPERIMENTAL_MISTRALRS=1 on the Host to opt in")
		}
		if len(m.RPCServers) != 0 {
			return backendLaunchSpec{}, errors.New("mistral.rs does not support NodexLM GGML RPC Guests; use llama.cpp for distributed inference")
		}
		if m.MoECPU || m.MoECPULayers != 0 || m.MoECacheMiB != 0 || m.MoECacheCPUShare != 0 ||
			(m.FlashAttention != "" && m.FlashAttention != "auto") ||
			(m.KVCacheType != "" && m.KVCacheType != "f16") ||
			m.BatchSize != 0 || m.UBatchSize != 0 || m.CacheReuseTokens != 0 || m.SpeculativeTokens != 0 ||
			m.ThreadsBatch != 0 || m.PromptCacheMiB != 0 || m.LoadMode != "" && m.LoadMode != "auto" || m.LazyMode != "" && m.LazyMode != "auto" || m.NUMA != "" ||
			(m.SpeculativeMode != "" && m.SpeculativeMode != "off") || m.DraftModelPath != "" {
			return backendLaunchSpec{}, errors.New("mistral.rs does not support the selected llama.cpp MoE, Flash Attention, KV-cache, batch or speculative options; reset them before switching")
		}
		if gpu != "auto" && gpu != "all" && gpu != "0" {
			return backendLaunchSpec{}, errors.New("mistral.rs adapter supports GPU auto/all or CPU-only (gpu_layers=0), not fixed llama.cpp layer counts")
		}
		if meta.PoolingType > 0 {
			return backendLaunchSpec{}, errors.New("embedding GGUFs are not enabled in the experimental mistral.rs adapter")
		}
		if strings.Contains(strings.ToLower(filepath.Base(m.ModelPath)), "-00001-of-") {
			return backendLaunchSpec{}, errors.New("sharded GGUF is not supported in the experimental mistral.rs adapter")
		}
		binary := m.Binary
		if binary == "" {
			binary = os.Getenv("NODEXLM_MISTRALRS_BIN")
		}
		if binary == "" {
			binary = "mistralrs"
		}
		resolved, err := exec.LookPath(binary)
		if err != nil {
			return backendLaunchSpec{}, fmt.Errorf("mistral.rs binary unavailable (%s): %w", binary, err)
		}
		// See docs.mistralrs.dev/reference/cli/serve; default host is
		// 0.0.0.0, so the explicit 127.0.0.1 is security critical.
		args := []string{"serve", "--host", "127.0.0.1", "--port", strconv.Itoa(port),
			"--no-ui", "--max-model-len", strconv.Itoa(nctx), "-f", m.ModelPath}
		if gpu == "0" {
			args = append(args, "--cpu")
		}
		return backendLaunchSpec{Binary: resolved, Args: args, ReadyPath: "/v1/models",
			Timeout: 5 * time.Minute}, nil
	default:
		return backendLaunchSpec{}, fmt.Errorf("unsupported inference backend %q", m.Backend)
	}
}

// mistral.rs /health may return OK while the model is still loading.
// Only report ready when a model is explicitly marked loaded.
func backendIsReady(backend string, response *http.Response) bool {
	if response.StatusCode != http.StatusOK {
		return false
	}
	if backend != BackendMistralRS {
		return true
	}
	var payload struct {
		Data []struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&payload); err != nil {
		return false
	}
	for _, m := range payload.Data {
		if m.Status == "loaded" {
			return true
		}
	}
	return false
}

func (m *Managed) normalizeModel(req *protocol.ChatRequest) {
	if EffectiveBackend(m.Backend) == BackendMistralRS {
		req.Model = "default"
	}
}

// Rewrite only the model identifier inside authenticated user requests. Do not
// modify tools/messages or expose the upstream, unauthenticated HTTP API.
func (m *Managed) normalizeOpenAI(body []byte) ([]byte, error) {
	if EffectiveBackend(m.Backend) != BackendMistralRS {
		return body, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, errors.New("OpenAI request must be a JSON object")
	}
	payload["model"] = json.RawMessage(`"default"`)
	return json.Marshal(payload)
}

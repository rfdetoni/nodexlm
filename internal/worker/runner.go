// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/rfdetoni/nodexlm/internal/config"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/netclient"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

type Generator interface {
	Generate(context.Context, protocol.ChatRequest) (string, error)
}

type Runner struct {
	Client *netclient.Client
	Engine Generator
	Model  string
	Logger *slog.Logger
}

func Enroll(ctx context.Context, coordinator, joinToken, name, model string) (config.Agent, error) {
	c, err := netclient.New(coordinator, joinToken, "")
	if err != nil {
		return config.Agent{}, err
	}
	if name == "" {
		return config.Agent{}, errors.New("name required")
	}
	dev := protocol.Device{Name: name, Platform: runtime.GOOS, Architecture: runtime.GOARCH, CPUCount: runtime.NumCPU()}
	if model != "" {
		dev.Models = []string{model}
	}
	var cred protocol.Credential
	_, err = c.Request(ctx, http.MethodPost, "/v1/nodes/enroll", protocol.Enrollment{Device: dev}, &cred)
	if err != nil {
		return config.Agent{}, err
	}
	return config.Agent{Coordinator: coordinator, Node: cred}, nil
}
func (r *Runner) Run(ctx context.Context) error {
	if r.Logger == nil {
		r.Logger = slog.Default()
	}
	if r.Model == "" || r.Engine == nil {
		return errors.New("worker requires a model and local llama-server backend")
	}
	delay := time.Second
	deviceName, _ := os.Hostname()
	var profile protocol.Device
	var sampledAt time.Time
	for ctx.Err() == nil {
		if time.Since(sampledAt) >= 15*time.Second {
			profile = hardware.Collect(deviceName, []string{r.Model})
			profile.Runtime.Installed = true
			sampledAt = time.Now()
		}
		var work protocol.Job
		queryCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		status, err := r.Client.Request(queryCtx, http.MethodPost, "/v1/nodes/lease", profile, &work)
		cancel()
		if err != nil {
			r.Logger.Warn("coordinator temporarily unavailable", "error", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay + time.Duration(rand.Intn(500))*time.Millisecond):
			}
			if delay < 20*time.Second {
				delay *= 2
			}
			continue
		}
		delay = time.Second
		if status == http.StatusNoContent {
			continue
		}
		r.Logger.Info("inference job started", "job_id", work.ID, "model", work.Request.Model)
		jobCtx, stop := context.WithTimeout(ctx, 3*time.Minute)

		monitorDone := make(chan struct{})
		go func() {
			defer close(monitorDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-jobCtx.Done():
					return
				case <-ticker.C:
					var state struct {
						Status string `json:"status"`
					}
					pollCtx, cancel := context.WithTimeout(jobCtx, 5*time.Second)
					status, err := r.Client.Request(pollCtx, http.MethodGet, "/v1/nodes/jobs/"+work.ID, nil, &state)
					cancel()
					if status == http.StatusUnauthorized || status == http.StatusNotFound || err == nil && state.Status != "running" {
						stop()
						return
					}
				}
			}
		}()
		var answer string
		var generateErr error
		if work.Request.Stream {
			if engine, ok := r.Engine.(interface {
				GenerateStream(context.Context, protocol.ChatRequest, func([]byte) error) error
			}); ok {
				generateErr = engine.GenerateStream(jobCtx, work.Request, func(frame []byte) error {
					payload := bytes.TrimSpace(bytes.TrimPrefix(frame, []byte("data: ")))
					if bytes.Equal(payload, []byte("[DONE]")) {
						return nil
					}
					_, err := r.Client.Request(jobCtx, http.MethodPost, "/v1/nodes/chunks/"+work.ID, map[string]any{"data": json.RawMessage(payload)}, nil)
					return err
				})
			} else {
				generateErr = errors.New("worker engine does not support streaming")
			}
		} else {
			answer, generateErr = r.Engine.Generate(jobCtx, work.Request)
		}

		stop()
		<-monitorDone
		out := boundedResult(answer, generateErr)
		resultCtx, done := context.WithTimeout(ctx, 15*time.Second)
		_, err = r.Client.Request(resultCtx, http.MethodPost, "/v1/nodes/result/"+work.ID, out, nil)
		done()
		if err != nil {
			r.Logger.Error("could not submit result", "job_id", work.ID, "error", err)
		}
	}
	return nil
}

// boundedResult enforces BOTH the coordinator's 512 KiB text ceiling and
// its 1 MiB HTTP JSON body limit. Escape-heavy text can be much larger on
// the wire; check its actual encoding without reducing valid ASCII limits.
func boundedResult(answer string, generateErr error) protocol.JobResult {
	if generateErr != nil {
		message := generateErr.Error()
		if len(message) > 1000 {
			message = message[:1000]
		}
		return protocol.JobResult{Error: message}
	}
	const maxResultBytes = 1 << 19
	result := protocol.JobResult{Result: answer}
	if len(answer) <= maxResultBytes {
		if encoded, err := json.Marshal(result); err == nil && len(encoded) <= 1<<20 {
			return result
		}
	}
	return protocol.JobResult{Error: "inference result exceeds coordinator limit; reduce max_tokens"}
}

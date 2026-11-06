// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"math/rand"
	"net/http"
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
		r.Logger.Info("inference job started", "job", work.ID, "model", work.Request.Model)
		jobCtx, stop := context.WithTimeout(ctx, 3*time.Minute)
		answer, generateErr := r.Engine.Generate(jobCtx, work.Request)
		stop()
		out := protocol.JobResult{Result: answer}
		if generateErr != nil {
			out.Error = generateErr.Error()
			if len(out.Error) > 1000 {
				out.Error = out.Error[:1000]
			}
		}
		resultCtx, done := context.WithTimeout(ctx, 15*time.Second)
		_, err = r.Client.Request(resultCtx, http.MethodPost, "/v1/nodes/result/"+work.ID, out, nil)
		done()
		if err != nil {
			r.Logger.Error("could not submit result", "job", work.ID, "error", err)
		}
	}
	return nil
}

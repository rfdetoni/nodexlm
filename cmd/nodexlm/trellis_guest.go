package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// GPU candidates never fall back to a stock binary with a different ABI.
// The policy enables experimentation; successful native computation is
// the admission gate for actual hardware.
func provisionTrellisGuest(home string, profile protocol.Device, allowGPU bool) (backend, worker, devices string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	if allowGPU {
		backend = autoGuestBackend(profile)
		if backend == "cuda" || backend == "vulkan" || backend == "metal" {
			gpuCtx, stop := context.WithTimeout(ctx, 10*time.Minute)
			_, worker, err = llamaruntime.EnsureTrellisRPCBackend(gpuCtx, home, backend)
			if err == nil {
				names := nativeRPCDeviceNames(worker)
				devices = autoRPCDeviceChoice(names, profile)
				if backend == "cuda" {
					devices, err = explicitCUDADeviceChoice(names, profile)
				}
				if err == nil && devices != "" && devices != "CPU" {
					err = llamaruntime.VerifyTrellisDevices(gpuCtx, worker, devices)
					if err == nil {
						stop()
						return backend, worker, devices, nil
					}
				}
			}
			stop()
			slog.Warn("Trellis GPU candidate unavailable; selecting matching CPU runtime", "backend", backend, "error", err)
		}
	}
	_, worker, err = llamaruntime.EnsureTrellisRPC(ctx, home)
	return "cpu", worker, "CPU", err
}

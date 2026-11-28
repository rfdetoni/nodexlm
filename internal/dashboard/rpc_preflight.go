package dashboard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

// preflightRPCGuests validates the selected native workers through the local
// authenticated relays *before* starting llama-server. CONNECT plus HELLO
// is insufficient: the actual GGML buffer allocation, SET_TENSOR and
// GET_TENSOR roundtrip must also work before attempting a multi-GB upload.
// Probes are concurrent across nodes and serial per native device.
func preflightRPCGuests(ctx context.Context, selected []clusterplan.Candidate, endpoints []string, available ...RPCGuest) error {
	return preflightRPCGuestsRuntime(ctx, false, selected, endpoints, available...)
}

func preflightRPCGuestsRuntime(ctx context.Context, trellis bool, selected []clusterplan.Candidate, endpoints []string, available ...RPCGuest) error {
	if len(selected) != len(endpoints) {
		return fmt.Errorf("RPC preflight requires one authenticated relay per selected Guest")
	}
	errs := make([]error, len(endpoints))
	var wg sync.WaitGroup
	for i, endpoint := range endpoints {
		wg.Add(1)
		go func(i int, endpoint string) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			var expected []uint64
			var name string
			var kinds []string
			for _, guest := range available {
				if guest.ID == selected[i].ID {
					name, kinds = guest.Name, guest.RPCDeviceKinds
					if !validRPCDeviceKinds(guest) {
						break
					}
					for _, kind := range guest.RPCDeviceKinds {
						budget := uint64(0)
						if kind == "gpu" && rpcGPUCount(guest) > 1 {
							budget = rpcDeviceBudget(guest, kind)
						}
						expected = append(expected, budget)
					}
				}
			}
			label := name
			if label == "" {
				label = fmt.Sprintf("Guest %.12s", selected[i].ID)
			}
			count, err := retryRPCPreflight(checkCtx, func() (uint32, error) {
				if trellis {
					return rpcmesh.ProbeTrellisTensorUpload(checkCtx, endpoint)
				}
				return rpcmesh.ProbeTensorUpload(checkCtx, endpoint, expected...)
			}, func(attempt int, err error) {
				slog.Warn("Guest native RPC preflight interrupted; retrying fresh tunnel", "guest_id", selected[i].ID, "guest_name", name, "attempt", attempt, "error", err)
				benchmarkPhase(checkCtx, "preflight_retry", fmt.Sprintf("%s · attempt %d/3 · %v", label, attempt, err))
			})
			if err != nil {
				errs[i] = fmt.Errorf("%s (%.12s, devices %v): native RPC 64-KiB preflight failed; inspect Guest native logs and GPU driver or memory pressure: %w", label, selected[i].ID, kinds, err)
				slog.Warn("Guest native RPC preflight failed", "guest_id", selected[i].ID, "guest_name", name, "devices", kinds, "error", err)
				return
			}
			slog.Info("Guest native RPC tensor upload/readback preflight passed", "guest_id", selected[i].ID, "guest_name", name, "devices", count)
			benchmarkPhase(checkCtx, "preflight_ok", fmt.Sprintf("%s · %d device(s)", label, count))
		}(i, endpoint)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	var failed []string
	var causes []error
	for i, err := range errs {
		if err != nil {
			failed = append(failed, selected[i].ID)
			causes = append(causes, err)
		}
	}
	if len(failed) > 0 {
		return &rpcPlacementFailure{guests: failed, cause: errors.Join(causes...)}
	}
	return nil
}

// Retry only transport interruption. Invalid protocol, device count, memory
// admission and readback corruption are terminal. Each attempt opens a new
// authenticated relay connection; never reuse partially consumed native RPC.
func retryRPCPreflight(ctx context.Context, probe func() (uint32, error), report func(int, error)) (uint32, error) {
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		count, err := probe()
		if err == nil || attempt == 3 || !retryableRPCPreflight(err) {
			return count, err
		}
		report(attempt+1, err)
		timer := time.NewTimer(time.Duration(attempt) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
}

func retryableRPCPreflight(err error) bool {
	var network net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNREFUSED) || (errors.As(err, &network) && network.Timeout())
}

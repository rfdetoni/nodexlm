package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

// preflightRPCGuests validates the selected native workers through the local
// authenticated relays *before* starting llama-server. A CONNECT tunnel by
// itself does not establish protocol readiness. Probes are concurrent so a
// defective Guest cannot delay healthy ones. The Host reports exactly which
// Guest stalled instead of waiting for a generic inference HTTP timeout.
func preflightRPCGuests(ctx context.Context, selected []clusterplan.Candidate, endpoints []string) error {
	if len(selected) != len(endpoints) {
		return fmt.Errorf("RPC preflight requires one authenticated relay per selected Guest")
	}
	errs := make([]error, len(endpoints))
	var wg sync.WaitGroup
	for i, endpoint := range endpoints {
		wg.Add(1)
		go func(i int, endpoint string) {
			defer wg.Done()
			checkCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			count, err := rpcmesh.ProbeHello(checkCtx, endpoint)
			if err != nil {
				errs[i] = fmt.Errorf("Guest %.12s cannot complete GGML RPC HELLO/DEVICE_COUNT: %w", selected[i].ID, err)
				slog.Warn("Guest native RPC preflight failed", "guest_id", selected[i].ID, "error", err)
				return
			}
			slog.Info("Guest native RPC preflight passed", "guest_id", selected[i].ID, "devices", count)
		}(i, endpoint)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

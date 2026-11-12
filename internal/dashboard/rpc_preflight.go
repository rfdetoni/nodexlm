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
// authenticated relays *before* starting llama-server. CONNECT plus HELLO
// is insufficient: the actual GGML buffer allocation, SET_TENSOR and
// GET_TENSOR roundtrip must also work before attempting a multi-GB upload.
// Probes are concurrent across nodes and serial per native device.
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
			checkCtx, cancel := context.WithTimeout(ctx, 22*time.Second)
			defer cancel()
			count, err := rpcmesh.ProbeTensorUpload(checkCtx, endpoint)
			if err != nil {
				errs[i] = fmt.Errorf("Guest %.12s failed native GGML RPC handshake or 64-KiB tensor upload/readback (device allocate, set, get): %w", selected[i].ID, err)
				slog.Warn("Guest native RPC preflight failed", "guest_id", selected[i].ID, "error", err)
				return
			}
			slog.Info("Guest native RPC tensor upload/readback preflight passed", "guest_id", selected[i].ID, "devices", count)
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

package rpcmesh

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/netclient"
)

// RunAttachedGuest reuses the authenticated RPC tunnel protocol for a native
// backend owned by the application (iOS cannot spawn rpc-server processes).
// The application owns native readiness, heartbeat and shutdown.
func RunAttachedGuest(ctx context.Context, client *netclient.Client, address string, slots int) error {
	if err := ValidateRPCAddress(address); err != nil {
		return err
	}
	if slots < 1 || slots > 8 {
		return errors.New("invalid tunnel count")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	fatal := make(chan error, 1)
	for range slots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			delay := time.Second
			for ctx.Err() == nil {
				err := Connect(ctx, client, address)
				if errors.Is(err, ErrGuestCredentialsRejected) {
					select {
					case fatal <- err:
					default:
					}
					return
				}
				if ctx.Err() != nil {
					return
				}
				if err == nil {
					delay = time.Second
				} else {
					client.ReconnectFailures.Add(1)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(jitteredRPCBackoff(delay)):
				}
				delay = nextRPCBackoff(delay, err)
			}
		}()
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-fatal:
		err = fmt.Errorf("RPC Guest must be re-paired: %w", err)
	}
	cancel()
	wg.Wait()
	return err
}

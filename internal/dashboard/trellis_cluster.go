package dashboard

import (
	"context"
	"errors"
	"time"
)

func (m *Manager) resetTrellisRPC() {
	m.mu.Lock()
	mode := m.trellisRPCMode
	m.mu.Unlock()
	if mode != nil {
		mode(false)
	}
}

func waitTrellisGuests(ctx context.Context, provider func() []RPCGuest) error {
	return waitRuntimeGuests(ctx, provider, 20*time.Minute)
}

func waitRuntimeGuests(ctx context.Context, provider func() []RPCGuest, limit time.Duration) error {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(provider()) != 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("no compatible RPC Guest finished provisioning")
		case <-ticker.C:
		}
	}
}

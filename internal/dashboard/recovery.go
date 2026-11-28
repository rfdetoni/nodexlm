package dashboard

import (
	"context"
	"errors"
	"log/slog"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

type rpcPlacementFailure struct {
	guests []string
	cause  error
}

func (e *rpcPlacementFailure) Error() string { return e.cause.Error() }
func (e *rpcPlacementFailure) Unwrap() error { return e.cause }

func filterRPCGuests(guests []RPCGuest, excluded map[string]bool) []RPCGuest {
	out := make([]RPCGuest, 0, len(guests))
	for _, guest := range guests {
		if !excluded[guest.ID] {
			out = append(out, guest)
		}
	}
	return out
}

// Detect only observable relay loss, not idle capacity or transient latency.
// Established native sessions must not be silently reused after a relay epoch
// change or the disappearance of all previously claimed connections.
func lostRPCGuests(nodes []string, before, after map[string]rpcmesh.TunnelStats, established bool) []string {
	var failed []string
	for _, id := range nodes {
		old, known := before[id]
		now, present := after[id]
		if !known || old.Epoch == 0 {
			continue
		}
		if !present || now.Epoch != old.Epoch || now.Connected == 0 ||
			(established && old.Connected > old.Idle && now.Connected == now.Idle) {
			failed = append(failed, id)
		}
	}
	return failed
}

func (m *Manager) loadWithReplanning(ctx context.Context) error {
	excluded := make(map[string]bool)
	for attempt := 0; ; attempt++ {
		err := m.load(ctx, excluded)
		var failure *rpcPlacementFailure
		if err == nil || ctx.Err() != nil || attempt >= 2 || !errors.As(err, &failure) {
			return err
		}
		changed := false
		for _, id := range failure.guests {
			if !excluded[id] {
				excluded[id] = true
				changed = true
			}
		}
		if !changed {
			return err
		}
		m.mu.Lock()
		explicit := explicitPrimary(m.settings.PrimaryNode)
		m.loadRPCNodes = nil
		m.loadUploadPlan = nil
		m.mu.Unlock()
		if explicit {
			return err
		}
		m.setPhase("replanning_guests")
		slog.Warn("Replanning model load after RPC failure", "attempt", attempt+1, "excluded", len(excluded))
	}
}

// Serialize recovery with an automatic model replacement. No request is
// replayed: recovery is invoked only before the next generation starts.
func (m *Manager) recoverPrimary(ctx context.Context, model string) error {
	m.mu.Lock()
	engine, current := m.engine, m.current
	provider := m.rpcStatsProvider
	baseline := m.loadedRPCSessions
	nodes := append([]string(nil), m.loadRPCNodes...)
	eligible := !m.closed && !m.loading && !m.unloading && engine != nil &&
		(model == "" || model == current) && len(engine.RPCServers) > 0
	m.mu.Unlock()
	if !eligible {
		return nil
	}
	lost := func() bool {
		return engine.ProcessExited() || (provider != nil && len(lostRPCGuests(nodes, baseline, provider(), true)) > 0)
	}
	if !lost() {
		return nil
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	m.mu.Lock()
	same := m.engine == engine && !m.closed && !m.loading && !m.unloading
	m.mu.Unlock()
	if !same || !lost() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	slog.Warn("Recovering primary model from confirmed native RPC session loss", "model", current)
	m.Unload()
	return m.Load(ctx)
}

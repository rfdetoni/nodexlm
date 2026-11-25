// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

func (m *Manager) microbenchPath(id string) string {
	if len(id) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(id); err != nil {
		return ""
	}
	return filepath.Join(m.dir, "microbench-"+id+".json")
}

func (m *Manager) microbenchSample(id string) *rpcmesh.Microbenchmark {
	path := m.microbenchPath(id)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var sample rpcmesh.Microbenchmark
	if json.Unmarshal(raw, &sample) != nil || sample.Source != "auto" ||
		time.Since(sample.MeasuredAt) >= 24*time.Hour || sample.MeasuredAt.After(time.Now()) ||
		len(sample.Devices) == 0 || len(sample.Devices) > 4 {
		return nil
	}
	return &sample
}

// AutoMicrobench is called asynchronously on authenticated heartbeat/tunnel
// registration. Skip when disabled, busy or already recently attempted.
func (m *Manager) AutoMicrobench(id string) {
	if os.Getenv("NODEXLM_AUTO_RPC_MICROBENCH") != "1" || m.microbenchSample(id) != nil {
		return
	}
	m.mu.Lock()
	if m.microbenchAttempts == nil {
		m.microbenchAttempts = make(map[string]time.Time)
	}
	if m.closed || time.Since(m.microbenchAttempts[id]) < time.Minute {
		m.mu.Unlock()
		return
	}
	for key, at := range m.microbenchAttempts {
		if time.Since(at) >= time.Minute {
			delete(m.microbenchAttempts, key)
		}
	}
	if len(m.microbenchAttempts) >= 128 {
		m.mu.Unlock()
		return
	}
	m.microbenchAttempts[id] = time.Now()
	m.mu.Unlock()
	_, _ = m.MeasureRPCDevice(context.Background(), id)
}

// MeasureRPCDevice owns the model pool and never runs while any model is
// resident. The provider excludes draining/offline Guests; cancellation
// closes the native connection promptly when the node becomes unavailable.
func (m *Manager) MeasureRPCDevice(ctx context.Context, id string) (rpcmesh.Microbenchmark, error) {
	path := m.microbenchPath(id)
	if path == "" || !m.poolMu.TryLock() {
		return rpcmesh.Microbenchmark{}, errors.New("invalid Guest or inference pool busy")
	}
	defer m.poolMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m.mu.Lock()
	if m.closed || m.loading || m.unloading || m.engine != nil || len(m.residents) > 0 || m.rpcEndpoints == nil {
		m.mu.Unlock()
		return rpcmesh.Microbenchmark{}, errors.New("unload models before RPC microbenchmark")
	}
	provider := m.rpcEndpoints
	m.poolCancel = cancel
	m.microbenchNode = id
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.poolCancel = nil; m.microbenchNode = ""; m.mu.Unlock() }()
	var endpoint string
	for _, guest := range provider() {
		if guest.ID == id && guest.CapacityBytes >= 64<<20 {
			endpoint = guest.Endpoint
			break
		}
	}
	if endpoint == "" {
		return rpcmesh.Microbenchmark{}, errors.New("Guest unavailable or insufficient memory")
	}
	// Drain callbacks cancel the native probe even after it claims a tunnel.
	result, err := rpcmesh.Microbench(ctx, endpoint)
	if err != nil {
		return result, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	return result, writePrivate(path, raw)
}

func (m *Manager) CancelRPCMicrobench(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.microbenchNode == id && m.poolCancel != nil {
		m.poolCancel()
	}
}

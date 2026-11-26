package safedownload

import (
	"context"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type meterKey struct{}

// ConnectionProgress contains only local staging names and range positions.
// No source URL, credentials, or server headers reach the dashboard.
type ConnectionProgress struct {
	ID       uint64 `json:"id"`
	File     string `json:"file"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	Received int64  `json:"received"`
}

type measuredConnection struct {
	info ConnectionProgress
	n    atomic.Int64
}

type TransferMeter struct {
	mu       sync.Mutex
	next     uint64
	active   map[uint64]*measuredConnection
	bytes    atomic.Int64
	sampled  time.Time
	previous int64
	rate     int64
}

func NewTransferMeter() *TransferMeter {
	return &TransferMeter{active: make(map[uint64]*measuredConnection), sampled: time.Now()}
}

func WithTransferMeter(ctx context.Context, meter *TransferMeter) context.Context {
	return context.WithValue(ctx, meterKey{}, meter)
}

func measureConnection(ctx context.Context, file string, start, end int64) (func(int), func()) {
	m, _ := ctx.Value(meterKey{}).(*TransferMeter)
	if m == nil {
		return func(int) {}, func() {}
	}
	m.mu.Lock()
	m.next++
	id := m.next
	c := &measuredConnection{info: ConnectionProgress{ID: id, File: filepath.Base(file), Start: start, End: end}}
	m.active[id] = c
	m.mu.Unlock()
	return func(n int) {
		c.n.Add(int64(n))
		m.bytes.Add(int64(n))
	}, func() {
		m.mu.Lock()
		delete(m.active, id)
		m.mu.Unlock()
	}
}

// Snapshot computes actual accepted body throughput, including retry traffic,
// independently of durable range progress. Sampling does not spawn timers.
func (m *TransferMeter) Snapshot() ([]ConnectionProgress, int64) {
	if m == nil {
		return nil, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if elapsed := now.Sub(m.sampled); elapsed >= 500*time.Millisecond {
		n := m.bytes.Load()
		m.rate = int64(float64(n-m.previous) / elapsed.Seconds())
		m.sampled, m.previous = now, n
	}
	out := make([]ConnectionProgress, 0, len(m.active))
	for _, c := range m.active {
		v := c.info
		v.Received = c.n.Load()
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, m.rate
}

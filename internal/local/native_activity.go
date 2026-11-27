// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const (
	nativeTraceLimit       = 16
	nativeMeasurementBasis = "native_synchronized_wall_us"
	nativeTraceLimitations = "Includes GGML scheduler, RPC wait, and diagnostic trace overhead; does not represent exclusive GPU execution time or SSE inter-token delay."
)

// NativeTokenActivity reports successful native graph submissions, not device
// utilization or per-device execution time. DecodeMicroseconds is the native
// synchronized decode wall time, including scheduling, RPC and trace overhead.
type NativeTokenActivity struct {
	ProcessEpoch       string               `json:"process_epoch,omitempty"`
	Batch              uint64               `json:"batch"`
	SlotID             int                  `json:"slot_id"`
	Ordinal            uint64               `json:"ordinal"`
	Splits             []NativeBackendSplit `json:"splits"`
	DecodeMicroseconds *uint64              `json:"decode_microseconds,omitempty"`
	MeasurementBasis   string               `json:"measurement_basis"`
	Limitations        string               `json:"limitations,omitempty"`
}

type NativeBackendSplit struct {
	Backend string `json:"backend"`
	Count   int    `json:"count"`
	NodeID  string `json:"node_id,omitempty"`
}

type nativeTrace struct {
	mu                 sync.Mutex
	processEpoch       string
	line               [1024]byte
	used               int
	discard            bool
	batch              uint64
	splits             []NativeBackendSplit
	complete           bool
	decodeMicroseconds *uint64
	valid              bool
	recent             []NativeTokenActivity
}

// Write is attached only to stderr and only when tracing is explicitly enabled.
// Long or malformed lines are discarded. No background worker or unbounded
// line/batch storage is needed even if no dashboard is connected.
func (t *nativeTrace) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			if !t.discard && bytes.HasPrefix(t.line[:t.used], []byte("NODEXLM_NATIVE_")) {
				t.consume(strings.TrimSpace(string(t.line[:t.used])))
			}
			t.used, t.discard = 0, false
			continue
		}
		if t.discard {
			continue
		}
		if t.used == len(t.line) {
			t.discard = true
			continue
		}
		t.line[t.used] = b
		t.used++
	}
	return len(p), nil
}

func (t *nativeTrace) next(batch uint64) bool {
	if batch == 0 || batch < t.batch {
		return false
	}
	if batch != t.batch {
		t.batch = batch
		t.splits = t.splits[:0]
		t.complete, t.valid = false, true
		t.decodeMicroseconds = nil
	}
	return true
}

func (t *nativeTrace) consume(line string) {
	var batch, ordinal uint64
	var split, nodes, inputs, status, output, task, slot, attributable int
	switch {
	case strings.HasPrefix(line, "NODEXLM_NATIVE_SPLIT "):
		head, backend, ok := strings.Cut(line, " backend=")
		n, _ := fmt.Sscanf(head, "NODEXLM_NATIVE_SPLIT batch=%d split=%d nodes=%d", &batch, &split, &nodes)
		if n != 3 || !t.next(batch) || t.complete {
			return
		}
		if !ok || split < 0 || nodes <= 0 || len(backend) == 0 || len(backend) > 512 ||
			strings.IndexFunc(backend, func(r rune) bool { return r < 32 || r > 126 }) >= 0 {
			t.valid = false
			return
		}
		backend = sanitizeTraceBackend(backend)
		for i := range t.splits {
			if t.splits[i].Backend == backend {
				t.splits[i].Count++
				return
			}
		}
		if len(t.splits) >= 32 {
			t.valid = false
			return
		}
		t.splits = append(t.splits, NativeBackendSplit{Backend: backend, Count: 1})
	case strings.HasPrefix(line, "NODEXLM_NATIVE_BATCH "):
		head, duration, hasDuration := strings.Cut(line, " decode_us=")
		n, _ := fmt.Sscanf(head, "NODEXLM_NATIVE_BATCH batch=%d inputs=%d status=%d output=%d", &batch, &inputs, &status, &output)
		if n == 4 && t.next(batch) && !t.complete {
			t.complete = true
			t.valid = t.valid && inputs == 1 && status == 0 && output == 1
			if hasDuration {
				us, err := strconv.ParseUint(duration, 10, 64)
				if err != nil {
					t.valid = false
				} else {
					t.decodeMicroseconds = &us
				}
			}
		}
	case strings.HasPrefix(line, "NODEXLM_NATIVE_TOKEN "):
		n, _ := fmt.Sscanf(line, "NODEXLM_NATIVE_TOKEN batch=%d task=%d slot=%d ordinal=%d attributable=%d", &batch, &task, &slot, &ordinal, &attributable)
		if n != 5 || batch != t.batch || !t.complete || !t.valid ||
			attributable != 1 || ordinal <= 1 || len(t.splits) == 0 {
			return
		}
		if t.processEpoch == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			t.processEpoch = hex.EncodeToString(b[:])
		}
		entry := NativeTokenActivity{
			ProcessEpoch:       t.processEpoch,
			Batch:              batch,
			SlotID:             slot,
			Ordinal:            ordinal,
			Splits:             append([]NativeBackendSplit(nil), t.splits...),
			DecodeMicroseconds: t.decodeMicroseconds,
			MeasurementBasis:   nativeMeasurementBasis,
			Limitations:        nativeTraceLimitations,
		}
		if len(t.recent) == nativeTraceLimit {
			copy(t.recent, t.recent[1:])
			t.recent = t.recent[:nativeTraceLimit-1]
		}
		t.recent = append(t.recent, entry)
		t.valid = false
	}
}

func (t *nativeTrace) snapshot() []NativeTokenActivity {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]NativeTokenActivity(nil), t.recent...)
	for i := range out {
		out[i].Splits = append([]NativeBackendSplit(nil), out[i].Splits...)
		if out[i].DecodeMicroseconds != nil {
			us := *out[i].DecodeMicroseconds
			out[i].DecodeMicroseconds = &us
		}
	}
	return out
}

// NativeActivity is empty for uninstrumented runtimes or unsupported decode
// modes. Each process has its own bounded history.
func (m *Managed) NativeActivity() []NativeTokenActivity {
	if t := m.nativeActivity.Load(); t != nil {
		return t.snapshot()
	}
	return nil
}

// sanitizeTraceBackend redacts private relay socket paths or unmapped endpoints
// so host filesystem locations never leak into telemetry.
func sanitizeTraceBackend(backend string) string {
	lower := strings.ToLower(backend)
	if strings.Contains(lower, "/tmp/") || strings.Contains(lower, "nodexlm-relay-") ||
		strings.Contains(lower, "unix:") || strings.Contains(lower, "rpc.sock") {
		return "unknown/unattributed"
	}
	return backend
}

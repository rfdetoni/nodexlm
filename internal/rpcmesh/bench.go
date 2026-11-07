package rpcmesh

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"time"
)

type Measurement struct {
	NodeID       string    `json:"node_id"`
	MeasuredAt   time.Time `json:"measured_at"`
	RoundTripMS  float64   `json:"round_trip_ms"`
	MiBPerSecond float64   `json:"mib_per_second"`
	Bytes        int       `json:"bytes"`
}

// Benchmark uses an idle authenticated TLS tunnel, never the native GGML
// stream. Marker 2 is advertised only by Guests supporting this operation.
func (h *Hub) Benchmark(ctx context.Context, id string) (Measurement, error) {
	h.mu.Lock()
	r := h.nodes[id]
	h.mu.Unlock()
	if r == nil {
		return Measurement{}, ErrTunnelUnavailable
	}
	conn := r.takeIdle()
	if conn == nil {
		return Measurement{}, errors.New("no idle tunnel; stop inference before benchmarking")
	}
	defer r.finish(conn)
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, err := conn.Write([]byte{2}); err != nil {
		return Measurement{}, err
	}
	exchange := func(size int) (time.Duration, error) {
		data := bytes.Repeat([]byte{0xa5}, size)
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(size))
		start := time.Now()
		if _, err := conn.Write(header[:]); err != nil {
			return 0, err
		}
		done := make(chan error, 1)
		go func() { _, err := io.Copy(conn, bytes.NewReader(data)); done <- err }()
		echoed := make([]byte, size)
		_, readErr := io.ReadFull(conn, echoed)
		if readErr != nil {
			_ = conn.Close()
		}
		writeErr := <-done
		if readErr != nil {
			return 0, readErr
		}
		if writeErr != nil {
			return 0, writeErr
		}
		if !bytes.Equal(data, echoed) {
			return 0, errors.New("benchmark echo corrupted")
		}
		return time.Since(start), nil
	}
	ping, err := exchange(1)
	if err != nil {
		return Measurement{}, err
	}
	elapsed, err := exchange(1 << 20)
	if err != nil {
		return Measurement{}, err
	}
	return Measurement{NodeID: id, MeasuredAt: time.Now(), RoundTripMS: float64(ping) / float64(time.Millisecond), MiBPerSecond: 2 / elapsed.Seconds(), Bytes: 2 << 20}, nil
}

func echoBenchmark(input io.Reader, output io.Writer) error {
	for range 2 {
		var size uint32
		if err := binary.Read(input, binary.BigEndian, &size); err != nil {
			return err
		}
		if size == 0 || size > 1<<20 {
			return errors.New("invalid benchmark length")
		}
		if _, err := io.CopyN(output, input, int64(size)); err != nil {
			return err
		}
	}
	return nil
}

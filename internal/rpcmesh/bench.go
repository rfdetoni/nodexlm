package rpcmesh

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"time"
)

type Measurement struct {
	NodeID                 string    `json:"node_id"`
	MeasuredAt             time.Time `json:"measured_at"`
	RoundTripMS            float64   `json:"round_trip_ms"`
	MiBPerSecond           float64   `json:"mib_per_second"`
	Bytes                  int       `json:"bytes"`
	Samples                int       `json:"samples,omitempty"`
	UploadMiBPerSecond     float64   `json:"upload_mib_per_second,omitempty"`
	DownloadMiBPerSecond   float64   `json:"download_mib_per_second,omitempty"`
	DirectionalSamples     int       `json:"directional_samples,omitempty"`
	DirectionalBytes       int       `json:"directional_bytes,omitempty"`
	RTTSpreadMS            float64   `json:"rtt_spread_ms,omitempty"`
	EchoSpreadMiBPerSecond float64   `json:"echo_spread_mib_per_second,omitempty"`
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
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Each legacy marker-2 session accepts exactly two exchanges.
	// Use fresh authenticated tunnels for warmup and each measured pair.
	var rtt, rates, uploads, downloads []float64
	var last Measurement
	// Leave existing busy sessions alone. With just one idle slot, wait
	// for its normal authenticated replacement after warmup.
	pairs := min(3, max(1, r.idleCount()-1))
	for i := 0; i <= pairs; i++ {
		var conn net.Conn
		for conn == nil {
			if candidate := r.takeIdle(); candidate != nil {
				conn = candidate
				break
			}
			select {
			case <-ctx.Done():
				return Measurement{}, fmt.Errorf("RPC benchmark: waiting for fresh idle tunnel: %w", ctx.Err())
			case <-r.closed:
				return Measurement{}, ErrTunnelUnavailable
			case <-r.ready:
			}
		}
		pairCtx, pairCancel := context.WithTimeout(ctx, 5*time.Second)
		sample, err := benchmarkPair(pairCtx, id, conn)
		pairCancel()
		r.finish(conn)
		if err != nil {
			return Measurement{}, fmt.Errorf("RPC benchmark pair %d/%d (5s limit, 15s total): %w", i+1, pairs+1, err)
		}
		if i > 0 {
			rtt = append(rtt, sample.RoundTripMS)
			rates = append(rates, sample.MiBPerSecond)
			if sample.DirectionalSamples > 0 {
				uploads = append(uploads, sample.UploadMiBPerSecond)
				downloads = append(downloads, sample.DownloadMiBPerSecond)
			}
			last = sample
		}
	}
	slices.Sort(rtt)
	slices.Sort(rates)
	last.RoundTripMS, last.MiBPerSecond = medianSorted(rtt), medianSorted(rates)
	last.RTTSpreadMS, last.EchoSpreadMiBPerSecond = rtt[len(rtt)-1]-rtt[0], rates[len(rates)-1]-rates[0]
	last.Samples, last.Bytes = len(rtt), len(rtt)*(2<<20)
	last.UploadMiBPerSecond, last.DownloadMiBPerSecond = 0, 0
	last.DirectionalSamples, last.DirectionalBytes = len(uploads), len(uploads)*(2<<20)
	if len(uploads) > 0 {
		slices.Sort(uploads)
		slices.Sort(downloads)
		last.UploadMiBPerSecond, last.DownloadMiBPerSecond = medianSorted(uploads), medianSorted(downloads)
	}
	return last, nil
}

func medianSorted(values []float64) float64 {
	return (values[(len(values)-1)/2] + values[len(values)/2]) / 2
}

func benchmarkPair(ctx context.Context, id string, conn net.Conn) (Measurement, error) {
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return Measurement{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	marker := byte(2)
	if caps, ok := conn.(*capabilityConn); ok && caps.directional && os.Getenv("NODEXLM_RPC_DIRECTIONAL_BENCH") == "1" {
		marker = 3
	}
	if _, err := conn.Write([]byte{marker}); err != nil {
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
	sample := Measurement{NodeID: id, MeasuredAt: time.Now(), RoundTripMS: float64(ping) / float64(time.Millisecond), MiBPerSecond: 2 / elapsed.Seconds(), Bytes: 2 << 20}
	if marker == 3 {
		upload, download, err := directionalHost(conn)
		if err != nil {
			return Measurement{}, err
		}
		sample.UploadMiBPerSecond, sample.DownloadMiBPerSecond = upload, download
		sample.DirectionalSamples, sample.DirectionalBytes = 1, 2<<20
	}
	return sample, nil
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

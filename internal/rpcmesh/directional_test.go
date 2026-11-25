// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestDirectionalCapabilityPreservesLegacyEcho(t *testing.T) {
	t.Setenv("NODEXLM_RPC_DIRECTIONAL_BENCH", "1")
	for _, supported := range []bool{false, true} {
		host, guest := net.Pipe()
		done := make(chan error, 1)
		go func() {
			defer guest.Close()
			var marker [1]byte
			_, err := io.ReadFull(guest, marker[:])
			if err == nil {
				if (marker[0] == 3) != supported {
					t.Error("incorrect negotiated marker")
				}
				err = echoBenchmark(guest, guest)
			}
			if err == nil && marker[0] == 3 {
				err = directionalGuest(guest, guest)
			}
			done <- err
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var conn net.Conn = host
		if supported {
			conn = &capabilityConn{Conn: host, directional: true}
		}
		sample, err := benchmarkPair(ctx, "test", conn)
		cancel()
		host.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if sample.MiBPerSecond <= 0 || (sample.DirectionalSamples == 1) != supported || supported && (sample.UploadMiBPerSecond <= 0 || sample.DownloadMiBPerSecond <= 0) {
			t.Fatalf("invalid negotiated measurement: %+v", sample)
		}
	}
}

func TestBenchmarkRejectsInvalidClockWithoutNonFiniteRates(t *testing.T) {
	for _, elapsed := range []time.Duration{0, -time.Millisecond} {
		if rate, err := measuredRate(2<<20, elapsed); rate != 0 || !errors.Is(err, errBenchmarkClock) {
			t.Fatalf("invalid clock: rate=%v err=%v", rate, err)
		}
	}
	if rate, err := measuredRate(2<<20, 500*time.Millisecond); err != nil || rate != 4 {
		t.Fatalf("valid rate: %v %v", rate, err)
	}
}

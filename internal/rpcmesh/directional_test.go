// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"context"
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
			conn = &directionalConn{Conn: host}
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

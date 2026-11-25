// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/netclient"
)

func TestEncryptedRPCTunnelTransportsBinary(t *testing.T) {
	rpc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.Close()
	go func() {
		for {
			c, err := rpc.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	hub := NewHub()
	defer hub.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Nodex-Node") != "test" || r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", 401)
			return
		}
		hub.ServeTunnel(w, r, "test")
	}))
	defer server.Close()
	cert := server.TLS.Certificates[0].Certificate[0]
	sum := sha256.Sum256(cert)
	client, err := netclient.New(server.URL, "secret", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = client.UsePinnedCertificate(hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Connect(ctx, client, rpc.Addr().String()) }()
	var endpoint string
	for i := 0; i < 100; i++ {
		eps := hub.Endpoints()
		if len(eps) > 0 {
			endpoint = eps[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if endpoint == "" {
		t.Fatal("tunnel never became ready")
	}
	remote, err := net.DialTimeout("tcp", endpoint, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = remote.SetDeadline(time.Now().Add(3 * time.Second))
	input := []byte{0, 1, 2, 3, 255, 0, 10, 27, 99}
	if _, err = remote.Write(input); err != nil {
		t.Fatal(err)
	}
	actual := make([]byte, len(input))
	if _, err = io.ReadFull(remote, actual); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", actual) != fmt.Sprintf("%x", input) {
		t.Fatalf("unexpected output: %x", actual)
	}
	// The receiver can read the echo before the sender's Write returns and
	// updates its counters. Wait for the asynchronous relay measurement.
	deadline := time.Now().Add(time.Second)
	for {
		traffic := hub.Stats()["test"]
		if traffic.BytesToGuest >= uint64(len(input)) && traffic.BytesFromGuest >= uint64(len(input)) &&
			traffic.LastActivityMS != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("real RPC bytes were not measured: %+v", traffic)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, idle := hub.IdleNodes()["test"]; idle {
		t.Fatal("busy Guest tunnel without idle slots must not be selected for a new model load")
	}
	if _, connected := hub.ReadyNodes()["test"]; !connected {
		t.Fatal("busy tunnel should still count as a connected Guest")
	}
	remote.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("tunnel did not exit")
	}
}
func TestRejectNonLocalRPC(t *testing.T) {
	for _, s := range []string{"0.0.0.0:50052", "192.168.1.3:50052", "example.com:50052"} {
		if err := ValidateRPCAddress(s); err == nil {
			t.Errorf("accepted nonloopback endpoint %s", s)
		}
	}
}

func TestRelayReclaimsDisconnectedIdleTunnels(t *testing.T) {
	hub := NewHub()
	defer hub.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeTunnel(w, r, "node")
	}))
	defer server.Close()

	cert, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	open := func() net.Conn {
		t.Helper()
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second},
			"tcp", server.Listener.Addr().String(),
			&tls.Config{RootCAs: roots, ServerName: "127.0.0.1", NextProtos: []string{"http/1.1"}})
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, err = fmt.Fprintf(conn, "CONNECT /v1/nodes/rpc/tunnel HTTP/1.1\r\nHost: %s\r\n\r\n",
			server.Listener.Addr().String())
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			conn.Close()
			t.Fatalf("expected HTTP 200 CONNECT, got %d", res.StatusCode)
		}
		_ = conn.SetDeadline(time.Time{})
		return conn
	}

	conns := make([]net.Conn, 0, 8)
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()
	for i := 0; i < 8; i++ {
		conns = append(conns, open())
	}
	// No Host-side RPC connection consumes these slots. Closing Guest sockets
	// must still release their capacity without waiting for a model load.
	_ = conns[0].Close()
	_ = conns[1].Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		hub.mu.Lock()
		count := hub.nodes["node"].count()
		hub.mu.Unlock()
		if count == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle Guest disconnects never released capacity: %d", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
	conns = append(conns, open(), open()) // Regression: previously HTTP 503.
	hub.mu.Lock()
	count := hub.nodes["node"].count()
	hub.mu.Unlock()
	if count != 8 {
		t.Fatalf("expected eight live tunnels after reconnect, got %d", count)
	}
}

func TestIdleClaimPreservesFirstByte(t *testing.T) {
	host, guest := net.Pipe()
	defer host.Close()
	defer guest.Close()
	r := &relay{ready: make(chan struct{}, 1), closed: make(chan struct{}), peers: map[net.Conn]struct{}{host: {}}}
	pending := &idleTunnel{conn: host, claimed: make(chan struct{}), stopped: make(chan struct{})}
	r.waiting = []*idleTunnel{pending}
	go r.watchIdle(pending)
	claimed := r.takeIdle()
	if claimed == nil {
		t.Fatal("claim lost idle connection")
	}
	go func() { _, _ = guest.Write([]byte{0x42}) }()
	_ = claimed.SetReadDeadline(time.Now().Add(time.Second))
	var first [1]byte
	if _, err := io.ReadFull(claimed, first[:]); err != nil || first[0] != 0x42 {
		t.Fatalf("lost first byte: %x %v", first, err)
	}
}

func TestLegacyEchoBenchmarkMedian(t *testing.T) {
	for _, slots := range []int{1, 2, 3, 8} {
		t.Run(fmt.Sprint(slots), func(t *testing.T) {
			hub := NewHub()
			defer hub.Close()
			r := &relay{ready: make(chan struct{}, 1), closed: make(chan struct{}), peers: make(map[net.Conn]struct{})}
			hub.nodes["test"] = r
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			r.listener = listener
			var add func(bool)
			add = func(replenish bool) {
				host, guest := net.Pipe()
				r.track(host)
				pending := &idleTunnel{conn: host, claimed: make(chan struct{}), stopped: make(chan struct{})}
				r.enqueue(pending)
				go r.watchIdle(pending)
				go func() {
					defer guest.Close()
					var marker [1]byte
					if _, err := io.ReadFull(guest, marker[:]); err == nil && marker[0] == 2 {
						if echoBenchmark(guest, guest) == nil && replenish {
							add(false)
						}
					}
				}()
			}
			for range slots {
				add(slots == 1)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got, err := hub.Benchmark(ctx, "test")
			want := min(3, max(1, slots-1))
			if err != nil || got.Samples != want || got.Bytes != want*(2<<20) || got.MiBPerSecond <= 0 {
				t.Fatalf("legacy echo median: %+v %v", got, err)
			}
			if want == 1 && (got.RTTSpreadMS != 0 || got.EchoSpreadMiBPerSecond != 0) {
				t.Fatalf("single sample has nonzero spread: %+v", got)
			}
		})
	}
	if medianSorted([]float64{2, 4}) != 3 || medianSorted([]float64{2}) != 2 || medianSorted([]float64{1, 2, 9}) != 2 {
		t.Fatal("incorrect median")
	}
}

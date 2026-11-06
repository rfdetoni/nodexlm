// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"context"
	"crypto/sha256"
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

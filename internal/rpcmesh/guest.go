// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/rfdetoni/nodexlm/internal/netclient"
)

// Connect creates one outgoing secure, pinned, authenticated connection from a
// Guest to its Host. Native RPC is contacted only when an inference starts.
func Connect(ctx context.Context, client *netclient.Client, rpcAddress string) error {
	if err := ValidateRPCAddress(rpcAddress); err != nil {
		return err
	}
	if client.NodeID == "" || client.Token == "" {
		return errors.New("Guest enrollment required")
	}
	u, err := url.Parse(client.Base)
	if err != nil || u.Scheme != "https" {
		return errors.New("RPC tunnels require HTTPS")
	}
	transport, ok := client.HTTP.Transport.(*http.Transport)
	if !ok {
		return ErrTunnelUnavailable
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()}
	if transport.TLSClientConfig != nil {
		cfg = transport.TLSClientConfig.Clone()
		cfg.ServerName = u.Hostname()
	}
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return err
	}
	secured := tls.Client(raw, cfg)
	if err = secured.HandshakeContext(ctx); err != nil {
		secured.Close()
		return err
	}
	defer secured.Close()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			secured.Close()
		case <-finished:
		}
	}()
	// No user-controlled HTTP paths or header values are interpolated.
	req, err := http.NewRequest(http.MethodConnect, client.Base+"/v1/nodes/rpc/tunnel", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+client.Token)
	req.Header.Set("X-Nodex-Node", client.NodeID)
	if err = req.Write(secured); err != nil {
		return err
	}
	reader := bufio.NewReader(secured)
	response, err := http.ReadResponse(reader, req)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC tunnel refused: HTTP %d", response.StatusCode)
	}
	// Marker is sent by coordinator only when llama.cpp connects to the relay.
	marker, err := reader.ReadByte()
	if err != nil {
		return err
	}
	if marker != 1 {
		return errors.New("invalid RPC relay handshake")
	}
	native, err := net.DialTimeout("tcp", rpcAddress, 3*time.Second)
	if err != nil {
		return fmt.Errorf("local rpc-server: %w", err)
	}
	defer native.Close()
	bridge(native, &buffered{Conn: secured, reader: reader})
	return nil
}

type buffered struct {
	net.Conn
	reader *bufio.Reader
}

func (b *buffered) Read(p []byte) (int, error) { return b.reader.Read(p) }

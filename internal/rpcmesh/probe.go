// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// ProbeHello verifies the *native* GGML RPC worker through an authenticated
// local relay. Tunnel counts and heartbeats alone do not prove that a Guest can
// answer the protocol's first request.
//
// b12255: HELLO command=14, 24-byte capability payload; the response is
// an 8-byte little-endian length followed by 28 bytes. DEVICE_COUNT is
// command=15, 0-byte payload and an unsigned 32-bit response.
// Keep this probe in sync with internal/llamaruntime/revision.json.
func ProbeHello(ctx context.Context, endpoint string) (uint32, error) {
	if err := ValidateRPCAddress(endpoint); err != nil {
		return 0, fmt.Errorf("invalid private RPC relay: %w", err)
	}
	network, address := "tcp", endpoint
	if strings.HasPrefix(endpoint, "unix:") {
		network, address = "unix", strings.TrimPrefix(endpoint, "unix:")
	}
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, address)
	if err != nil {
		return 0, fmt.Errorf("connect RPC relay: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(6 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return 0, err
	}
	// ggml-rpc is a raw, little-endian protocol on all supported hosts.
	var hello [33]byte
	hello[0] = 14
	binary.LittleEndian.PutUint64(hello[1:9], 24)
	if err := writeRPCProbe(conn, hello[:]); err != nil {
		return 0, fmt.Errorf("send RPC HELLO: %w", err)
	}
	var header [8]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, fmt.Errorf("Guest did not answer RPC HELLO: %w", err)
	}
	if n := binary.LittleEndian.Uint64(header[:]); n != 28 {
		return 0, fmt.Errorf("unexpected RPC HELLO response length %d, expected 28", n)
	}
	var response [28]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil {
		return 0, fmt.Errorf("incomplete RPC HELLO response: %w", err)
	}
	if response[0] != 9 || response[1] != 0 {
		return 0, fmt.Errorf("incompatible GGML RPC protocol %d.%d.%d (Host requires 9.0)", response[0], response[1], response[2])
	}
	var countCommand [9]byte
	countCommand[0] = 15
	if err := writeRPCProbe(conn, countCommand[:]); err != nil {
		return 0, fmt.Errorf("request RPC DEVICE_COUNT: %w", err)
	}
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, fmt.Errorf("Guest did not answer RPC DEVICE_COUNT: %w", err)
	}
	if n := binary.LittleEndian.Uint64(header[:]); n != 4 {
		return 0, fmt.Errorf("unexpected RPC DEVICE_COUNT response length %d", n)
	}
	var devices [4]byte
	if _, err := io.ReadFull(conn, devices[:]); err != nil {
		return 0, fmt.Errorf("incomplete RPC DEVICE_COUNT response: %w", err)
	}
	count := binary.LittleEndian.Uint32(devices[:])
	if count == 0 || count > 64 {
		return 0, fmt.Errorf("Guest reported invalid RPC device count %d", count)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func writeRPCProbe(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("zero-byte RPC write")
		}
		data = data[n:]
	}
	return nil
}

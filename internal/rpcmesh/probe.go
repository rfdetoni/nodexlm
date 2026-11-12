// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"context"
	"bytes"
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
	return probeNativeRPC(ctx, endpoint, false)
}

// ProbeTensorUpload verifies the actual GGML data path, not only HELLO.
// A small, bounded buffer is allocated on each reported native device, a
// tensor is uploaded through SET_TENSOR and read back through GET_TENSOR.
// This detects ARMv7 framing bugs and broken Android Vulkan/CPU data paths
// before the Host attempts multi-gigabyte model weight allocations.
func ProbeTensorUpload(ctx context.Context, endpoint string) (uint32, error) {
	return probeNativeRPC(ctx, endpoint, true)
}

func probeNativeRPC(ctx context.Context, endpoint string, verifyTensor bool) (uint32, error) {
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
	if verifyTensor {
		deadline = time.Now().Add(20 * time.Second)
	}
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
	if verifyTensor {
		// The request is made on the SAME authenticated GGML connection as the
		// protocol handshake. A new tunnel would hide a broken upload path.
		for device := uint32(0); device < count; device++ {
			if err := probeNativeTensor(conn, device); err != nil {
				return 0, fmt.Errorf("RPC device %d failed tensor upload/readback: %w", device, err)
			}
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
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


const (
	rpcProbeTensorBytes = 64 << 10
	// ggml-rpc.cpp b12255 packs rpc_tensor with 4 dimensions, 10 sources,
	// 64 bytes of op parameters and a 64-byte name. The fixed layout is 296 B.
	// Keep in sync with internal/llamaruntime/revision.json.
	rpcProbeTensorWireSize = 296
)

func probeNativeTensor(conn net.Conn, device uint32) error {
	const tensorElements = rpcProbeTensorBytes / 4 // GGML_TYPE_F32
	// ALLOC_BUFFER (0) carries {uint32 device, uint64 size} packed.
	var alloc [12]byte
	binary.LittleEndian.PutUint32(alloc[:4], device)
	binary.LittleEndian.PutUint64(alloc[4:], rpcProbeTensorBytes)
	response, err := rpcProbeRoundtrip(conn, 0, alloc[:], 16)
	if err != nil {
		return fmt.Errorf("ALLOC_BUFFER: %w", err)
	}
	buffer := binary.LittleEndian.Uint64(response[:8])
	size := binary.LittleEndian.Uint64(response[8:])
	if buffer == 0 || size < rpcProbeTensorBytes {
		return fmt.Errorf("ALLOC_BUFFER returned no usable %d-byte allocation", rpcProbeTensorBytes)
	}
	var pointer [8]byte
	binary.LittleEndian.PutUint64(pointer[:], buffer)
	// Free only after the upload and readback complete.
	defer func() {
		_ = rpcProbeSend(conn, 4, pointer[:]) // FREE_BUFFER has no response
	}()
	response, err = rpcProbeRoundtrip(conn, 3, pointer[:], 8) // BUFFER_GET_BASE
	if err != nil {
		return fmt.Errorf("BUFFER_GET_BASE: %w", err)
	}
	base := binary.LittleEndian.Uint64(response)
	if base == 0 || base > ^uint64(0)-rpcProbeTensorBytes {
		return errors.New("BUFFER_GET_BASE returned an invalid native address")
	}

	// rpc_tensor's packed layout on GGML RPC v9 (see pinned b12255 source):
	// id@0; type@8; buffer@12; ne@20; nb@36; op@52;
	// op_params@56; flags@120; src@124; view_src@204;
	// view_offs@212; data@220; name@228; use_count@292.
	tensor := make([]byte, rpcProbeTensorWireSize)
	binary.LittleEndian.PutUint32(tensor[8:], 0) // GGML_TYPE_F32
	binary.LittleEndian.PutUint64(tensor[12:], buffer)
	for i, n := range []uint32{tensorElements, 1, 1, 1} {
		binary.LittleEndian.PutUint32(tensor[20+i*4:], n)
	}
	for i, n := range []uint32{4, rpcProbeTensorBytes, rpcProbeTensorBytes, rpcProbeTensorBytes} {
		binary.LittleEndian.PutUint32(tensor[36+i*4:], n)
	}
	binary.LittleEndian.PutUint64(tensor[220:], base)
	copy(tensor[228:292], "nodexlm-rpc-probe")

	pattern := make([]byte, rpcProbeTensorBytes)
	for i := range pattern {
		pattern[i] = byte(i*97 + 11)
	}
	// SET_TENSOR (6) carries rpc_tensor, cache_flag, offset, and raw payload.
	upload := make([]byte, 0, len(tensor)+9+len(pattern))
	upload = append(upload, tensor...)
	upload = append(upload, 0) // no disk cache or hashing
	upload = append(upload, make([]byte, 8)...) // offset 0
	upload = append(upload, pattern...)
	if err := rpcProbeSend(conn, 6, upload); err != nil {
		return fmt.Errorf("SET_TENSOR: %w", err)
	}
	// GET_TENSOR (8) forces native synchronization and proves bytes reached
	// the allocated GGML backend buffer, rather than only the TLS tunnel.
	read := make([]byte, 0, len(tensor)+16)
	read = append(read, tensor...)
	read = append(read, make([]byte, 8)...) // offset 0
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], rpcProbeTensorBytes)
	read = append(read, count[:]...)
	actual, err := rpcProbeRoundtrip(conn, 8, read, rpcProbeTensorBytes)
	if err != nil {
		return fmt.Errorf("GET_TENSOR: %w", err)
	}
	if !bytes.Equal(pattern, actual) {
		return errors.New("GET_TENSOR readback mismatch: native RPC tensor data was corrupted")
	}
	return nil
}

func rpcProbeSend(conn net.Conn, cmd byte, payload []byte) error {
	var header [9]byte
	header[0] = cmd
	binary.LittleEndian.PutUint64(header[1:], uint64(len(payload)))
	if err := writeRPCProbe(conn, header[:]); err != nil {
		return err
	}
	return writeRPCProbe(conn, payload)
}

func rpcProbeRoundtrip(conn net.Conn, cmd byte, payload []byte, expected uint64) ([]byte, error) {
	if err := rpcProbeSend(conn, cmd, payload); err != nil {
		return nil, err
	}
	var header [8]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, fmt.Errorf("no native RPC reply header: %w", err)
	}
	if n := binary.LittleEndian.Uint64(header[:]); n != expected {
		return nil, fmt.Errorf("native RPC response size %d, expected %d", n, expected)
	}
	response := make([]byte, expected)
	if _, err := io.ReadFull(conn, response); err != nil {
		return nil, fmt.Errorf("incomplete native RPC reply: %w", err)
	}
	return response, nil
}

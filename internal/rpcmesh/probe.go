// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
)

// ProbeHello verifies the *native* GGML RPC worker through an authenticated
// local relay. Tunnel counts and heartbeats alone do not prove that a Guest can
// answer the protocol's first request.
//
// v1.0.0: HELLO command=14, 24-byte capability payload; the response is
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
func ProbeTensorUpload(ctx context.Context, endpoint string, expectedGPUFree ...uint64) (uint32, error) {
	if len(expectedGPUFree) == 0 {
		return probeNativeRPC(ctx, endpoint, true)
	}
	return withRPCProbe(ctx, endpoint, 20*time.Second, func(conn net.Conn, count uint32) error {
		if int(count) != len(expectedGPUFree) {
			return fmt.Errorf("native device count %d does not match planned %d", count, len(expectedGPUFree))
		}
		for device, required := range expectedGPUFree {
			if required != 0 {
				var req [4]byte
				binary.LittleEndian.PutUint32(req[:], uint32(device))
				response, err := rpcProbeRoundtrip(conn, 11, req[:], 16)
				if err != nil {
					return err
				}
				free, total := binary.LittleEndian.Uint64(response[:8]), binary.LittleEndian.Uint64(response[8:])
				if free < required || free > total {
					return fmt.Errorf("native GPU %d free memory below admitted budget", device)
				}
			}
			if err := probeNativeTensor(conn, uint32(device)); err != nil {
				return err
			}
		}
		return ctx.Err()
	})
}

func probeNativeRPC(ctx context.Context, endpoint string, verifyTensor bool) (uint32, error) {
	timeout := 6 * time.Second
	if verifyTensor {
		timeout = 20 * time.Second
	}
	return withRPCProbe(ctx, endpoint, timeout, func(conn net.Conn, count uint32) error {
		if verifyTensor {
			for device := uint32(0); device < count; device++ {
				if err := probeNativeTensor(conn, device); err != nil {
					return fmt.Errorf("RPC device %d failed tensor upload/readback: %w", device, err)
				}
			}
		}
		return ctx.Err()
	})
}

func withRPCProbe(ctx context.Context, endpoint string, timeout time.Duration, run func(net.Conn, uint32) error) (uint32, error) {
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
	deadline := time.Now().Add(timeout)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
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
	major, minor := llamaruntime.RPCProtocolVersion()
	if response[0] != major || response[1] != minor {
		return 0, fmt.Errorf("incompatible GGML RPC protocol %d.%d.%d (Host requires %d.%d); update Host and Guest together", response[0], response[1], response[2], major, minor)
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
	if err := run(conn, count); err != nil {
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

const (
	rpcProbeTensorBytes = 64 << 10
	// ggml-rpc.cpp v1.0.0 packs rpc_tensor with 4 dimensions, 10 sources,
	// 64 bytes of op parameters and a 64-byte name. The fixed layout is 296 B.
	// Keep in sync with internal/llamaruntime/revision.json.
	rpcProbeTensorWireSize = 296
)

func probeNativeTensor(conn net.Conn, device uint32) error {
	_, err := measureNativeTensor(conn, device, rpcProbeTensorBytes)
	return err
}

func measureNativeTensor(conn net.Conn, device, tensorBytes uint32) (TensorMeasurement, error) {
	tensorElements := tensorBytes / 4 // GGML_TYPE_F32
	// ALLOC_BUFFER (0) carries {uint32 device, uint64 size} packed.
	var alloc [12]byte
	binary.LittleEndian.PutUint32(alloc[:4], device)
	binary.LittleEndian.PutUint64(alloc[4:], uint64(tensorBytes))
	response, err := rpcProbeRoundtrip(conn, 0, alloc[:], 16)
	if err != nil {
		return TensorMeasurement{}, fmt.Errorf("ALLOC_BUFFER: %w", err)
	}
	buffer := binary.LittleEndian.Uint64(response[:8])
	size := binary.LittleEndian.Uint64(response[8:])
	if buffer == 0 || size < uint64(tensorBytes) {
		return TensorMeasurement{}, fmt.Errorf("ALLOC_BUFFER returned no usable %d-byte allocation", tensorBytes)
	}
	var pointer [8]byte
	binary.LittleEndian.PutUint64(pointer[:], buffer)
	// Free only after the upload and readback complete.
	defer func() {
		_ = rpcProbeSend(conn, 4, pointer[:]) // FREE_BUFFER has no response
	}()
	response, err = rpcProbeRoundtrip(conn, 3, pointer[:], 8) // BUFFER_GET_BASE
	if err != nil {
		return TensorMeasurement{}, fmt.Errorf("BUFFER_GET_BASE: %w", err)
	}
	base := binary.LittleEndian.Uint64(response)
	if base == 0 || base > ^uint64(0)-uint64(tensorBytes) {
		return TensorMeasurement{}, errors.New("BUFFER_GET_BASE returned an invalid native address")
	}

	// rpc_tensor's packed layout on GGML RPC v10 (see pinned v1.0.0 source):
	// id@0; type@8; buffer@12; ne@20; nb@36; op@52;
	// op_params@56; flags@120; src@124; view_src@204;
	// view_offs@212; data@220; name@228; use_count@292.
	tensor := make([]byte, rpcProbeTensorWireSize)
	binary.LittleEndian.PutUint32(tensor[8:], 0) // GGML_TYPE_F32
	binary.LittleEndian.PutUint64(tensor[12:], buffer)
	for i, n := range []uint32{tensorElements, 1, 1, 1} {
		binary.LittleEndian.PutUint32(tensor[20+i*4:], n)
	}
	for i, n := range []uint32{4, tensorBytes, tensorBytes, tensorBytes} {
		binary.LittleEndian.PutUint32(tensor[36+i*4:], n)
	}
	binary.LittleEndian.PutUint64(tensor[220:], base)
	copy(tensor[228:292], "nodexlm-rpc-probe")

	// Keep Host-side working memory independent of the native tensor size.
	pattern := make([]byte, min(tensorBytes, uint32(rpcProbeTensorBytes)))
	for i := range pattern {
		pattern[i] = byte(i*97 + 11)
	}
	// SET_TENSOR (6) carries rpc_tensor, cache_flag, offset, and raw payload.
	upload := make([]byte, 0, len(tensor)+9)
	upload = append(upload, tensor...)
	upload = append(upload, make([]byte, 9)...) // no cache, offset 0
	var header [9]byte
	header[0] = 6
	binary.LittleEndian.PutUint64(header[1:], uint64(len(upload))+uint64(tensorBytes))
	started := time.Now()
	if err := writeRPCProbe(conn, header[:]); err != nil {
		return TensorMeasurement{}, fmt.Errorf("SET_TENSOR header: %w", err)
	}
	if err := writeRPCProbe(conn, upload); err != nil {
		return TensorMeasurement{}, fmt.Errorf("SET_TENSOR descriptor: %w", err)
	}
	for left := tensorBytes; left > 0; {
		chunk := min(left, uint32(len(pattern)))
		if err := writeRPCProbe(conn, pattern[:chunk]); err != nil {
			return TensorMeasurement{}, fmt.Errorf("SET_TENSOR: %w", err)
		}
		left -= chunk
	}
	// SET_TENSOR has no reply. An ordered RPC fences timed uploads.
	// The legacy small preflight needs only the GET_TENSOR readback.
	if tensorBytes > rpcProbeTensorBytes {
		if _, err := rpcProbeRoundtrip(conn, 3, pointer[:], 8); err != nil {
			return TensorMeasurement{}, fmt.Errorf("SET_TENSOR fence: %w", err)
		}
	}
	uploadTime := time.Since(started)
	read := make([]byte, 0, len(tensor)+16)
	read = append(read, tensor...)
	read = append(read, make([]byte, 8)...) // offset 0
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], uint64(tensorBytes))
	read = append(read, count[:]...)
	started = time.Now()
	if err := rpcProbeSend(conn, 8, read); err != nil {
		return TensorMeasurement{}, fmt.Errorf("GET_TENSOR: %w", err)
	}
	if _, err := io.ReadFull(conn, count[:]); err != nil {
		return TensorMeasurement{}, fmt.Errorf("GET_TENSOR header: %w", err)
	}
	if n := binary.LittleEndian.Uint64(count[:]); n != uint64(tensorBytes) {
		return TensorMeasurement{}, fmt.Errorf("GET_TENSOR returned %d bytes, expected %d", n, tensorBytes)
	}
	actual := make([]byte, len(pattern))
	corrupted := false
	for left := tensorBytes; left > 0; {
		chunk := min(left, uint32(len(actual)))
		if _, err := io.ReadFull(conn, actual[:chunk]); err != nil {
			return TensorMeasurement{}, fmt.Errorf("GET_TENSOR readback: %w", err)
		}
		if !bytes.Equal(pattern[:chunk], actual[:chunk]) {
			corrupted = true
		}
		left -= chunk
	}
	readTime := time.Since(started)
	if corrupted {
		return TensorMeasurement{}, errors.New("GET_TENSOR readback mismatch: native RPC tensor data was corrupted")
	}
	result := TensorMeasurement{Device: device, Bytes: tensorBytes}
	mib := float64(tensorBytes) / (1 << 20)
	result.UploadMiBPerSecond = mib / uploadTime.Seconds()
	result.ReadbackMiBPerSecond = mib / readTime.Seconds()
	return result, nil
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

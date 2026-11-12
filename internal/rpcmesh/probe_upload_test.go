package rpcmesh

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// A deterministic GGML RPC v9 fixture. The real native server runs on device;
// this verifies the exact wire format and rejects a silently corrupted tensor.
func serveProbeRPC(conn net.Conn, corrupt bool) error {
	defer conn.Close()
	var stored []byte
	device := uint32(0)
	buffer := uint64(0)
	for {
		var header [9]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		size := binary.LittleEndian.Uint64(header[1:])
		if size > 128<<10 {
			return fmt.Errorf("unexpected request size %d", size)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(conn, data); err != nil {
			return err
		}
		response := func(payload []byte) error {
			var length [8]byte
			binary.LittleEndian.PutUint64(length[:], uint64(len(payload)))
			if _, err := conn.Write(length[:]); err != nil {
				return err
			}
			_, err := conn.Write(payload)
			return err
		}
		switch header[0] {
		case 14: // HELLO 24-byte capabilities
			if len(data) != 24 {
				return fmt.Errorf("HELLO size %d", len(data))
			}
			resp := make([]byte, 28)
			resp[0] = 9 // protocol v9, not HTTP or v8
			if err := response(resp); err != nil {
				return err
			}
		case 15: // DEVICE_COUNT
			if len(data) != 0 {
				return errors.New("DEVICE_COUNT payload")
			}
			var count [4]byte
			binary.LittleEndian.PutUint32(count[:], 2) // Vulkan + CPU
			if err := response(count[:]); err != nil {
				return err
			}
		case 0: // ALLOC_BUFFER
			if len(data) != 12 {
				return fmt.Errorf("ALLOC_BUFFER size %d", len(data))
			}
			device = binary.LittleEndian.Uint32(data[:4])
			if device > 1 || binary.LittleEndian.Uint64(data[4:]) != rpcProbeTensorBytes {
				return errors.New("invalid ALLOC_BUFFER request")
			}
			buffer = uint64(0x10000) + uint64(device)*0x10000
			var info [16]byte
			binary.LittleEndian.PutUint64(info[:8], buffer)
			binary.LittleEndian.PutUint64(info[8:], rpcProbeTensorBytes)
			if err := response(info[:]); err != nil {
				return err
			}
		case 3: // BUFFER_GET_BASE
			if len(data) != 8 || binary.LittleEndian.Uint64(data) != buffer {
				return errors.New("wrong buffer pointer")
			}
			var base [8]byte
			binary.LittleEndian.PutUint64(base[:], 0x50000000+uint64(device)*0x10000)
			if err := response(base[:]); err != nil {
				return err
			}
		case 6: // SET_TENSOR (no response)
			if len(data) != rpcProbeTensorWireSize+9+rpcProbeTensorBytes {
				return fmt.Errorf("bad SET_TENSOR wire size: %d", len(data))
			}
			if binary.LittleEndian.Uint32(data[8:12]) != 0 ||
				binary.LittleEndian.Uint64(data[12:20]) != buffer ||
				binary.LittleEndian.Uint32(data[20:24]) != rpcProbeTensorBytes/4 ||
				binary.LittleEndian.Uint64(data[220:228]) != 0x50000000+uint64(device)*0x10000 {
				return errors.New("invalid serialized GGML tensor descriptor")
			}
			stored = append([]byte(nil), data[rpcProbeTensorWireSize+9:]...)
		case 8: // GET_TENSOR
			if len(data) != rpcProbeTensorWireSize+16 ||
				binary.LittleEndian.Uint64(data[rpcProbeTensorWireSize+8:]) != rpcProbeTensorBytes {
				return errors.New("invalid GET_TENSOR request")
			}
			if len(stored) == 0 {
				return errors.New("GET_TENSOR before upload")
			}
			result := append([]byte(nil), stored...)
			if corrupt {
				result[len(result)/2] ^= 1
			}
			if err := response(result); err != nil {
				return err
			}
		case 4: // FREE_BUFFER
			if len(data) != 8 || binary.LittleEndian.Uint64(data) != buffer {
				return errors.New("invalid FREE_BUFFER request")
			}
			buffer = 0
			stored = nil
		default:
			return fmt.Errorf("unexpected native RPC command %d", header[0])
		}
	}
}

func TestProbeTensorUploadNativeWireAndReadback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		corrupt     bool
		wantFailure bool
	}{
		{name: "V9-two-devices"},
		{name: "detect-corrupted-tensor", corrupt: true, wantFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, e := listener.Accept()
				if e != nil {
					done <- e
					return
				}
				done <- serveProbeRPC(conn, tc.corrupt)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			count, err := ProbeTensorUpload(ctx, listener.Addr().String())
			if tc.wantFailure {
				if err == nil {
					t.Fatal("corrupted GGML tensor was accepted")
				}
			} else if err != nil || count != 2 {
				t.Fatalf("native RPC upload/readback failed: count=%d err=%v", count, err)
			}
			_ = listener.Close()
			select {
			case e := <-done:
				if e != nil && !tc.wantFailure {
					t.Fatalf("fake RPC fixture: %v", e)
				}
			case <-time.After(time.Second):
				t.Fatal("probe left the native RPC connection open")
			}
		})
	}
}

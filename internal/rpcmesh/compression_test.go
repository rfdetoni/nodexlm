// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"bytes"
	"encoding/binary"
	"io"
	"math/rand/v2"
	"net"
	"testing"
	"time"
)

func TestCompressionNegotiationIsOptInAndLegacySafe(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	for _, tc := range []struct {
		header string
		on     bool
		want   byte
	}{
		{"", true, 1},
		{"directional-v1", true, 1},
		{"deflate-v10", true, 1},
		{"directional-v1,deflate-v1", false, 1},
		{"directional-v1,deflate-v1", true, compressedNativeMarker},
	} {
		if got := nativeTunnelMarker(negotiateTunnel(a, tc.header), tc.on); got != tc.want {
			t.Fatalf("%q enabled=%v: marker %d", tc.header, tc.on, got)
		}
	}
}

func TestFramedRPCPreservesMixedDataAndBoundsWire(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	_ = a.SetDeadline(time.Now().Add(3 * time.Second))
	_ = b.SetDeadline(time.Now().Add(3 * time.Second))
	stats := &relay{}
	sender := &framedConn{Conn: a, stats: stats}
	receiver := &framedConn{Conn: b}
	input := bytes.Repeat([]byte{42}, 2*maxRPCFrame)
	random := make([]byte, maxRPCFrame)
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range random {
		random[i] = byte(rng.Uint32())
	}
	input = append(input, random...)
	input = append(input, []byte("control")...)
	done := make(chan error, 1)
	go func() {
		_, err := sender.Write(input)
		done <- err
	}()
	output := make([]byte, len(input))
	if _, err := io.ReadFull(receiver, output); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(input, output) {
		t.Fatal("RPC stream changed after compression")
	}
	if wire := stats.framedWireToGuest.Load(); wire >= uint64(len(input)) || wire < maxRPCFrame {
		t.Fatalf("unexpected wire byte count %d", wire)
	}
	if stats.framedNativeToGuest.Load() != uint64(len(input)) {
		t.Fatal("native byte accounting changed")
	}
}

func TestFramedRPCRejectsOversizedAndInvalidDeflate(t *testing.T) {
	for _, tc := range []struct {
		raw, wire uint32
		data      []byte
	}{
		{maxRPCFrame + 1, 1, nil},
		{0, 0, nil},
		{8, 9, nil},
		{128, 3, []byte{0xff, 0xff, 0xff}},
	} {
		a, b := net.Pipe()
		_ = b.SetDeadline(time.Now().Add(time.Second))
		go func() {
			defer a.Close()
			var header [8]byte
			binary.BigEndian.PutUint32(header[:4], tc.raw)
			binary.BigEndian.PutUint32(header[4:], tc.wire)
			if writeRPCProbe(a, header[:]) == nil && len(tc.data) > 0 {
				_ = writeRPCProbe(a, tc.data)
			}
		}()
		receiver := &framedConn{Conn: b}
		_, err := receiver.Read(make([]byte, 1))
		b.Close()
		if err == nil {
			t.Fatalf("accepted invalid frame %+v", tc)
		}
	}
}

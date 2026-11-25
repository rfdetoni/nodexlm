// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
)

const compressedNativeMarker = 4
const maxRPCFrame = 64 << 10
const minCompressionFrame = 16 << 10

type capabilityConn struct {
	net.Conn
	directional bool
	compression bool
}

func negotiateTunnel(conn net.Conn, header string) net.Conn {
	caps := &capabilityConn{Conn: conn}
	for _, value := range strings.Split(header, ",") {
		switch strings.TrimSpace(value) {
		case "directional-v1":
			caps.directional = true
		case "deflate-v1":
			caps.compression = true
		}
	}
	if !caps.directional && !caps.compression {
		return conn
	}
	return caps
}

func nativeTunnelMarker(conn net.Conn, enabled bool) byte {
	if caps, ok := conn.(*capabilityConn); enabled && ok && caps.compression {
		return compressedNativeMarker
	}
	return 1
}

// A lossless, bounded stream wrapper around the already authenticated TLS
// connection. Each frame is independently encoded, with no shared dictionary
// or HTTP credentials in the compression context. Lengths are big endian:
// raw length, wire length, payload. Equal lengths mean uncompressed.
// One reader and one writer are owned by the existing bridge pumps.
type framedConn struct {
	net.Conn
	stats *relay

	readBuffer []byte
	encoded    []byte
	pending    []byte
	inflater   io.ReadCloser

	compressed bytes.Buffer
	deflater   *flate.Writer
}

func (c *framedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.pending) == 0 {
		var header [8]byte
		if _, err := io.ReadFull(c.Conn, header[:]); err != nil {
			return 0, err
		}
		raw := int(binary.BigEndian.Uint32(header[:4]))
		wire := int(binary.BigEndian.Uint32(header[4:]))
		if raw <= 0 || raw > maxRPCFrame || wire <= 0 || wire > raw {
			return 0, errors.New("invalid compressed RPC frame length")
		}
		if c.readBuffer == nil {
			c.readBuffer = make([]byte, maxRPCFrame)
		}
		data := c.readBuffer[:raw]
		if wire == raw {
			if _, err := io.ReadFull(c.Conn, data); err != nil {
				return 0, err
			}
		} else {
			if c.encoded == nil {
				c.encoded = make([]byte, maxRPCFrame)
			}
			if _, err := io.ReadFull(c.Conn, c.encoded[:wire]); err != nil {
				return 0, err
			}
			input := bytes.NewReader(c.encoded[:wire])
			if c.inflater == nil {
				c.inflater = flate.NewReader(input)
			} else if err := c.inflater.(flate.Resetter).Reset(input, nil); err != nil {
				return 0, err
			}
			_, err := io.ReadFull(c.inflater, data)
			if err == nil {
				var extra [1]byte
				n, end := io.ReadFull(c.inflater, extra[:])
				if n != 0 || end != io.EOF || input.Len() != 0 {
					err = errors.New("compressed RPC frame size mismatch")
				}
			}
			closeErr := c.inflater.Close()
			if err != nil {
				return 0, err
			}
			if closeErr != nil {
				return 0, closeErr
			}
		}
		c.pending = data
		if c.stats != nil {
			c.stats.framedWireFromGuest.Add(uint64(wire + len(header)))
			c.stats.framedNativeFromGuest.Add(uint64(raw))
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *framedConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		size := min(len(p), maxRPCFrame)
		data := p[:size]
		wire := data
		// Tiny control messages are sent immediately. Compression is never
		// required to succeed; retain it only if it saves at least 12.5%.
		if size >= minCompressionFrame {
			c.compressed.Reset()
			if c.deflater == nil {
				writer, err := flate.NewWriter(&c.compressed, flate.BestSpeed)
				if err != nil {
					return total, err
				}
				c.deflater = writer
			} else {
				c.deflater.Reset(&c.compressed)
			}
			_, err := c.deflater.Write(data)
			closeErr := c.deflater.Close()
			if err == nil && closeErr == nil && c.compressed.Len()+8 < size-size/8 {
				wire = c.compressed.Bytes()
			}
		}
		var header [8]byte
		binary.BigEndian.PutUint32(header[:4], uint32(size))
		binary.BigEndian.PutUint32(header[4:], uint32(len(wire)))
		if err := writeRPCProbe(c.Conn, header[:]); err != nil {
			return total, err
		}
		if err := writeRPCProbe(c.Conn, wire); err != nil {
			return total, err
		}
		if c.stats != nil {
			c.stats.framedWireToGuest.Add(uint64(len(wire) + len(header)))
			c.stats.framedNativeToGuest.Add(uint64(size))
		}
		total += size
		p = p[size:]
	}
	return total, nil
}

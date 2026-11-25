// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"bytes"
	"errors"
	"io"
	"net"
	"time"
)

const directionalSize = 1 << 20

func directionalPayload() []byte {
	return bytes.Repeat([]byte{0xa5}, 16<<10)
}

func writeDirectional(out io.Writer, data []byte) error {
	for remaining := directionalSize; remaining > 0; remaining -= len(data) {
		if _, err := io.Copy(out, bytes.NewReader(data)); err != nil {
			return err
		}
	}
	return nil
}

func readDirectional(in io.Reader, expected []byte) error {
	buffer := make([]byte, len(expected))
	for remaining := directionalSize; remaining > 0; remaining -= len(buffer) {
		if _, err := io.ReadFull(in, buffer); err != nil {
			return err
		}
		if !bytes.Equal(buffer, expected) {
			return errors.New("directional benchmark payload mismatch")
		}
	}
	return nil
}

func directionalGuest(in io.Reader, out io.Writer) error {
	payload := directionalPayload()
	if err := readDirectional(in, payload); err != nil {
		return err
	}
	if _, err := out.Write([]byte{0xa5}); err != nil {
		return err
	}
	var request [1]byte
	if _, err := io.ReadFull(in, request[:]); err != nil {
		return err
	}
	if request[0] != 0x5a {
		return errors.New("invalid directional download request")
	}
	return writeDirectional(out, payload)
}

func directionalHost(conn net.Conn) (float64, float64, error) {
	payload := directionalPayload()
	start := time.Now()
	if err := writeDirectional(conn, payload); err != nil {
		return 0, 0, err
	}
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return 0, 0, err
	}
	if ack[0] != 0xa5 {
		return 0, 0, errors.New("invalid directional upload acknowledgement")
	}
	upload := time.Since(start)
	start = time.Now()
	if _, err := conn.Write([]byte{0x5a}); err != nil {
		return 0, 0, err
	}
	if err := readDirectional(conn, payload); err != nil {
		return 0, 0, err
	}
	download := time.Since(start)
	if upload <= 0 || download <= 0 {
		return 0, 0, errors.New("invalid directional measurement duration")
	}
	return 1 / upload.Seconds(), 1 / download.Seconds(), nil
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package safedownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
)

type responseTransport func(*http.Request) (*http.Response, error)
func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type faultReader struct {
	src  io.Reader
	done bool
}
func (r *faultReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return r.src.Read(p)
	}
	return 0, io.ErrUnexpectedEOF
}

func TestStreamGGUFResumesInterruptedVerifiedTransfer(t *testing.T) {
	payload := append([]byte("GGUF"), bytes.Repeat([]byte{0x7a}, 1024)...)
	checksum := sha256.Sum256(payload)
	first := 240
	requests := 0
	client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		remaining := payload
		status := http.StatusOK
		header := make(http.Header)
		header.Set("X-Linked-Etag", hex.EncodeToString(checksum[:]))
		if requests == 1 {
			if req.Header.Get("Range") != "" {
				t.Fatal("first request unexpectedly attempted Range")
			}
			remaining = payload[:first]
		} else {
			if req.Header.Get("Range") != fmt.Sprintf("bytes=%d-", first) {
				t.Fatalf("unexpected Range: %q", req.Header.Get("Range"))
			}
			remaining = payload[first:]
			status = http.StatusPartialContent
			header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, len(payload)-1, len(payload)))
		}
		var body io.Reader = bytes.NewReader(remaining)
		if requests == 1 {
			body = &faultReader{src: body}
		}
		length := int64(len(payload))
		if requests > 1 {
			length = int64(len(remaining))
		}
		return &http.Response{
			StatusCode: status, Header: header, Body: io.NopCloser(body),
			ContentLength: length, Request: req,
		}, nil
	})}
	file, err := os.CreateTemp(t.TempDir(), "stream-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	count, err := StreamGGUF(context.Background(), client, "https://huggingface.co/file.gguf", file, 4096, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || count != int64(len(payload)) {
		t.Fatalf("range requests=%d, count=%d", requests, count)
	}
	actual, err := os.ReadFile(file.Name())
	if err != nil || !bytes.Equal(actual, payload) {
		t.Fatalf("incomplete resumed file: %v", err)
	}
}

func TestStreamGGUFRejectsWrongPartialOffset(t *testing.T) {
	payload := []byte("GGUFabcdefgh")
	checksum := sha256.Sum256(payload)
	requests := 0
	client := &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		header := make(http.Header)
		header.Set("X-Linked-Etag", hex.EncodeToString(checksum[:]))
		if requests == 1 {
			return &http.Response{StatusCode: 200, Header: header, ContentLength: int64(len(payload)),
				Body: io.NopCloser(&faultReader{src: bytes.NewReader(payload[:4])}), Request: req}, nil
		}
		header.Set("Content-Range", "bytes 0-7/12") // expected offset is 4
		return &http.Response{StatusCode: http.StatusPartialContent, Header: header,
			ContentLength: 8, Body: io.NopCloser(bytes.NewReader(payload[4:])), Request: req}, nil
	})}
	file, err := os.CreateTemp(t.TempDir(), "invalid-range-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = StreamGGUF(context.Background(), client, "https://huggingface.co/file.gguf", file, 4096, nil); err == nil {
		t.Fatal("malformed Content-Range accepted")
	} else if !errors.Is(err, io.ErrUnexpectedEOF) && err.Error() != "invalid HTTP Content-Range while resuming GGUF" {
		t.Fatalf("unexpected error: %v", err)
	}
}

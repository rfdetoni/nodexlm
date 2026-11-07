// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package safedownload

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// StreamGGUF copies a bounded model file to a private, initially empty staging
// file. Interrupted responses are resumed with HTTP Range only if the original
// response advertised a SHA-256 digest. A complete digest verification is
// required before returning; unverified data is never published to models/.
// A cancelled download stays cancelled, and a failed transfer is cleaned up by
// its caller. There is no whole-transfer timeout while bytes keep flowing.
func StreamGGUF(ctx context.Context, client *http.Client, source string, file *os.File,
	limit int64, progress func(received, total int64)) (int64, error) {
	if client == nil || file == nil || limit < 4 {
		return 0, errors.New("invalid download destination or limit")
	}
	if err := file.Truncate(0); err != nil {
		return 0, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	digest := sha256.New()
	var received int64
	var total int64 = -1
	var expected []byte
	var lastErr error
	buffer := make([]byte, 256<<10)
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		// With no immutable digest, a failed response must be restarted rather
		// than appending an unrelated version from a new request.
		if received > 0 && (expected == nil || total < 0) {
			if err := file.Truncate(0); err != nil {
				return 0, err
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return 0, err
			}
			received = 0
			total = -1
			digest.Reset()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("User-Agent", "NodexLM/0.1 (GGUF downloader)")
		if received > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", received))
		}
		response, err := client.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			continue
		}
		switch response.StatusCode {
		case http.StatusOK:
			if received > 0 {
				if err := file.Truncate(0); err != nil {
					response.Body.Close()
					return 0, err
				}
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					response.Body.Close()
					return 0, err
				}
				received = 0
				digest.Reset()
			}
			expected, _ = ExpectedSHA256(response.Header)
			total = response.ContentLength
		case http.StatusPartialContent:
			if received == 0 || expected == nil {
				response.Body.Close()
				return 0, errors.New("unexpected unverified partial response")
			}
			first, last, size, err := parseContentRange(response.Header.Get("Content-Range"))
			if err != nil || first != received || size != total || last < first ||
				response.ContentLength >= 0 && last-first+1 != response.ContentLength {
				response.Body.Close()
				return 0, errors.New("invalid HTTP Content-Range while resuming GGUF")
			}
		default:
			response.Body.Close()
			return 0, fmt.Errorf("model download HTTP %d", response.StatusCode)
		}
		if total > limit {
			response.Body.Close()
			return 0, errors.New("model exceeds download size limit")
		}
		// Never read beyond the remaining budget even if Content-Length lies.
		writer := &progressWriter{file: file, hash: digest, limit: limit,
			base: received, progress: progress, total: total}
		_, lastErr = io.CopyBuffer(writer, io.LimitReader(response.Body, limit-received+1), buffer)
		_ = response.Body.Close()
		received += writer.written
		if received > limit {
			return 0, errors.New("model exceeds download size limit")
		}
		if progress != nil {
			progress(received, total)
		}
		if lastErr == nil && total >= 0 && received != total {
			lastErr = io.ErrUnexpectedEOF
		}
		if lastErr == nil {
			if expected != nil && subtle.ConstantTimeCompare(digest.Sum(nil), expected) != 1 {
				return 0, errors.New("GGUF SHA-256 does not match LFS metadata")
			}
			var magic [4]byte
			if _, err := file.ReadAt(magic[:], 0); err != nil || string(magic[:]) != "GGUF" {
				return 0, errors.New("downloaded part has invalid GGUF header")
			}
			return received, nil
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if attempt < 2 {
			select {
			case <-time.After(time.Duration(attempt+1) * time.Second):
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
	}
	return 0, fmt.Errorf("model download failed after retries: %w", lastErr)
}

type progressWriter struct {
	file     *os.File
	hash     io.Writer
	limit    int64
	base     int64
	written  int64
	progress func(received, total int64)
	total    int64
	last     time.Time
}

func (w *progressWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.limit-(w.base+w.written) {
		return 0, errors.New("download exceeds size limit")
	}
	n, err := w.file.Write(p)
	if n > 0 {
		_, _ = w.hash.Write(p[:n])
		w.written += int64(n)
		if w.progress != nil && time.Since(w.last) >= 250*time.Millisecond {
			w.progress(w.base+w.written, w.total)
			w.last = time.Now()
		}
	}
	return n, err
}

// parseContentRange accepts only resolved "bytes START-END/TOTAL" responses
// and rejects wildcard sizes and malformed ranges.
func parseContentRange(header string) (first, last, total int64, err error) {
	if !strings.HasPrefix(header, "bytes ") {
		return 0, 0, 0, errors.New("missing bytes Content-Range")
	}
	parts := strings.Split(strings.TrimPrefix(header, "bytes "), "/")
	if len(parts) != 2 {
		return 0, 0, 0, errors.New("invalid Content-Range")
	}
	rng := strings.Split(parts[0], "-")
	if len(rng) != 2 {
		return 0, 0, 0, errors.New("invalid Content-Range offset")
	}
	first, err = strconv.ParseInt(rng[0], 10, 64)
	if err != nil {
		return
	}
	last, err = strconv.ParseInt(rng[1], 10, 64)
	if err != nil {
		return
	}
	total, err = strconv.ParseInt(parts[1], 10, 64)
	if err == nil && (first < 0 || last < first || total <= last) {
		err = errors.New("invalid Content-Range bounds")
	}
	return
}

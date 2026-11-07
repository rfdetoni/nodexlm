// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package safedownload

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestPublicIPRejectsPrivateAndReservedRanges(t *testing.T) {
	tests := []struct{ ip string; public bool }{
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},
		{"127.0.0.1", false},
		{"10.1.2.3", false},
		{"192.168.0.1", false},
		{"169.254.1.2", false},
		{"100.64.3.4", false},
		{"198.19.0.1", false},
		{"192.0.2.5", false},
		{"2001:db8::1", false},
		{"fd00::1", false},
		{"::1", false},
	}
	for _, tc := range tests {
		if got := publicIP(net.ParseIP(tc.ip)); got != tc.public {
			t.Errorf("%s: public=%v want %v", tc.ip, got, tc.public)
		}
	}
}

func TestPublicRedirectRestrictions(t *testing.T) {
	for _, value := range []string{
		"http://huggingface.co/file.gguf",
		"https://127.0.0.1:8443/file.gguf",
		"https://user:pass@huggingface.co/file.gguf",
		"https://huggingface.co:8443/file.gguf",
	} {
		u, err := url.Parse(value)
		if err != nil || ValidateURL(u) == nil {
			t.Errorf("accepted unsafe redirect: %s", value)
		}
	}
}

func TestExpectedLFSChecksum(t *testing.T) {
	h := http.Header{}
	h.Set("X-Linked-Etag", "\"" + hex.EncodeToString(sha256.New().Sum(nil)) + "\"")
	if b, ok := ExpectedSHA256(h); !ok || len(b) != sha256.Size {
		t.Fatal("valid LFS hash not recognized")
	}
	h.Set("X-Linked-Etag", "W/\"not-a-checksum\"")
	if _, ok := ExpectedSHA256(h); ok {
		t.Fatal("weak ETag treated as SHA-256")
	}
}

func TestIdleReadDeadlineStopsStalledTransfer(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	conn := &inactivityConn{Conn: client, idle: 25 * time.Millisecond}
	start := time.Now()
	var one [1]byte
	_, err := conn.Read(one[:])
	nerr, ok := err.(net.Error)
	if !ok || !nerr.Timeout() {
		t.Fatalf("stalled download did not time out: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("idle read deadline was not applied promptly")
	}
}

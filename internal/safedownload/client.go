// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
// Package safedownload restricts public model downloads to HTTPS and public IPs,
// including redirects. It must not be used for the paired LAN coordinator.
package safedownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

var excluded = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT / private overlays
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func publicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return false
	}
	for _, prefix := range excluded {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func ValidateURL(u *url.URL) error {
	if u == nil || u.Scheme != "https" || u.Hostname() == "" ||
		u.Port() != "" || u.User != nil || u.Fragment != "" {
		return errors.New("model downloads require public HTTPS without custom ports or credentials")
	}
	return nil
}

// inactivityConn bounds each blocked transport I/O independently. This is
// intentionally an idle timeout rather than a whole-response deadline: models
// can take hours on slow links as long as data continues to arrive.
type inactivityConn struct {
	net.Conn
	idle time.Duration
}

func (c *inactivityConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

func (c *inactivityConn) Write(p []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Write(p)
}

// Dial resolves each destination and dials only validated public addresses,
// preventing DNS rebinding between validation and connection establishment.
func Dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			continue
		}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return &inactivityConn{Conn: conn, idle: 90 * time.Second}, nil
		}
	}
	return nil, errors.New("no public download destination is reachable")
}

// Client enforces the same transport policy for dashboard and CLI downloads.
// Callers may provide their own client only in controlled tests.
func Client() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 8 {
				return errors.New("too many download redirects")
			}
			return ValidateURL(req.URL)
		},
		Transport: &http.Transport{
			Proxy: nil, DialContext: Dial, ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns: 2, MaxIdleConnsPerHost: 1, IdleConnTimeout: 10 * time.Second,
		},
	}
}

// ExpectedSHA256 extracts a content digest from Hugging Face LFS metadata.
// A weak/opaque ETag is not treated as a checksum.
func ExpectedSHA256(h http.Header) ([]byte, bool) {
	for _, key := range []string{"X-Linked-Etag", "ETag"} {
		value := strings.Trim(h.Get(key), " \"")
		if len(value) != sha256.Size*2 {
			continue
		}
		b, err := hex.DecodeString(value)
		if err == nil && len(b) == sha256.Size {
			return b, true
		}
	}
	return nil, false
}

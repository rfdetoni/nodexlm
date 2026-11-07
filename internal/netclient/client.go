// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package netclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type Client struct {
	ReconnectFailures atomic.Uint64
	Base              string
	Token             string
	NodeID            string
	HTTP              *http.Client
}

func New(base, token, nodeID string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid coordinator URL")
	}
	if u.Scheme != "https" {
		host := u.Hostname()
		if u.Scheme != "http" || !(host == "localhost" || host == "127.0.0.1" || host == "::1") {
			return nil, errors.New("HTTPS required outside localhost")
		}
	}
	dial := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	t := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: dial.DialContext, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 60 * time.Second, ResponseHeaderTimeout: 35 * time.Second, ForceAttemptHTTP2: true}
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, NodeID: nodeID, HTTP: &http.Client{Transport: t}}, nil
}
func (c *Client) Request(ctx context.Context, method, path string, input, output any) (int, error) {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return 0, err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if c.NodeID != "" {
		req.Header.Set("X-Nodex-Node", c.NodeID)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return resp.StatusCode, fmt.Errorf("server HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if output != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (c *Client) UseRoots(pool *x509.CertPool) error {
	t, ok := c.HTTP.Transport.(*http.Transport)
	if !ok {
		return errors.New("unsupported transport")
	}
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}
	return nil
}

// UsePinnedCertificate trusts only the exact enrolled coordinator certificate.
// InsecureSkipVerify disables default verification exclusively because the
// certificate is verified in VerifyConnection against the pre-authenticated pin.
func (c *Client) UsePinnedCertificate(hexDigest string) error {
	expected, err := hex.DecodeString(hexDigest)
	if err != nil || len(expected) != sha256.Size {
		return errors.New("invalid coordinator certificate pin")
	}
	t, ok := c.HTTP.Transport.(*http.Transport)
	if !ok {
		return errors.New("unsupported transport")
	}
	t.Proxy = nil // Paired LAN nodes must connect directly without a proxy.
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("coordinator did not present certificate")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
				return errors.New("coordinator TLS identity mismatch")
			}
			return nil
		},
	}
	return nil
}

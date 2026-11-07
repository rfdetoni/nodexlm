// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package pairing

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPairingCodeAndTLSProof(t *testing.T) {
	cert := make([]byte, 128)
	if _, err := rand.Read(cert); err != nil {
		t.Fatal(err)
	}
	s, code, err := NewSession(cert, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 8 || !ValidCode(code) {
		t.Fatalf("invalid code %q", code)
	}
	offer := s.Offer("https://127.0.0.1:8443")
	if err := VerifyOffer(code, offer, cert); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOffer(strings.Repeat("A", 8), offer, cert); err == nil && code != "AAAAAAAA" {
		t.Fatal("wrong code accepted")
	}
	fake := bytes.Clone(cert)
	fake[0] ^= 0xFF
	if VerifyOffer(code, offer, fake) == nil {
		t.Fatal("certificate substitution accepted")
	}
	offer.Proof = strings.Repeat("0", 64)
	if VerifyOffer(code, offer, cert) == nil {
		t.Fatal("tampered offer accepted")
	}
}
func TestPairingAttemptsAndExpiry(t *testing.T) {
	s, code, err := NewSession([]byte("certificate"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if s.Attempt("AAAAAAAA", "192.168.1.2") && code != "AAAAAAAA" {
			t.Fatal("bad code accepted")
		}
	}
	if s.Attempt(code, "192.168.1.2") {
		t.Fatal("source rate limit bypassed")
	}
	if !s.Attempt(code, "192.168.1.3") {
		t.Fatal("correct code rejected")
	}
	s.expires = time.Now().Add(-time.Second)
	if s.Attempt(code, "192.168.1.4") {
		t.Fatal("expired code accepted")
	}
}
func TestDiscoveryLoopbackAndTLSPin(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer ts.Close()
	c := ts.Certificate()
	s, code, err := NewSession(c.Raw, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The HTTPS endpoint advertises only public proof material.
	ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Offer(ts.URL))
	})
	fetched, der, err := FetchUntrustedOffer(context.Background(), ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyOffer(code, fetched, der); err != nil {
		t.Fatal(err)
	}
	der[0] ^= 1
	if VerifyOffer(code, fetched, der) == nil {
		t.Fatal("unauthenticated certificate accepted")
	}
	der[0] ^= 1
	// Check certificate parsing is compatible with the pin persisted by the client.
	var pemBuf bytes.Buffer
	_ = pem.Encode(&pemBuf, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	if _, err := x509.ParseCertificate(c.Raw); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.X509KeyPair(pemBuf.Bytes(), pemBuf.Bytes()); err == nil {
		t.Fatal("certificate used as private key")
	}
	// Verify UDP discovery is independent of HTTP and returns no actual code.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := sock.LocalAddr().(*net.UDPAddr).Port
	_ = sock.Close()
	done := make(chan error, 1)
	go func() { done <- ListenDiscovery(ctx, port, func() []Offer { return []Offer{s.Offer(ts.URL)} }) }()
	time.Sleep(50 * time.Millisecond)
	offers, err := Discover(context.Background(), port, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) == 0 {
		t.Fatal("LAN discovery returned no offers")
	}
	if strings.Contains(string(offers[0].Proof), code) {
		t.Fatal("pairing secret disclosed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("discovery did not stop")
	}
}

func TestPBKDF2CrossPlatformVector(t *testing.T) {
	key := pbkdf2SHA256("ABCD2345", []byte("1234567890123456"))
	want, _ := hex.DecodeString("50afef495a66abc32381ec8488d56a8787d5035336643c84fa015abcaacc2074")
	if !bytes.Equal(key[:], want) {
		t.Fatalf("PBKDF2 incompatible with standard library: %x", key)
	}
}

func TestUntrustedPairingOfferIsBoundedAndCancellable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 10<<10))
	}))
	defer server.Close()
	if _, _, err := FetchUntrustedOffer(context.Background(), server.URL); err == nil {
		t.Fatal("accepted oversized untrusted discovery payload")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := FetchUntrustedOffer(ctx, server.URL); err == nil {
		t.Fatal("cancelled pairing fetch did not stop")
	}
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func TestPeerGrantAndProofLifecycle(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	peerPriv, peerPubHex, err := GeneratePeerKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	grant, err := SignPeerGrant(priv, PeerGrant{
		ClusterID:    "cluster-a",
		Epoch:        3,
		SourceNodeID: "node-src",
		TargetPeerID: "node-dst",
		TargetPubKey: peerPubHex,
		Scope:        "cache",
		SHA256:       strings.Repeat("a", 64),
		Size:         1024,
		IssuedAt:     now,
		ExpiresAt:    now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPeerGrant(grant, pub, "cluster-a", 3, "node-src", "cache", now); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	if err := VerifyPeerGrant(grant, pub, "cluster-b", 3, "node-src", "cache", now); err == nil {
		t.Fatal("foreign cluster grant accepted")
	}
	if err := VerifyPeerGrant(grant, pub, "cluster-a", 4, "node-src", "cache", now); err == nil {
		t.Fatal("stale epoch grant accepted")
	}
	if err := VerifyPeerGrant(grant, pub, "cluster-a", 3, "node-other", "cache", now); err == nil {
		t.Fatal("source-substituted grant accepted")
	}
	if err := VerifyPeerGrant(grant, pub, "cluster-a", 3, "node-src", "cache", now.Add(3*time.Minute)); err == nil {
		t.Fatal("expired grant accepted")
	}

	seen := map[string]time.Time{}
	accept := func(nonce string, exp time.Time) bool {
		if _, exists := seen[nonce]; exists {
			return false
		}
		seen[nonce] = exp
		return true
	}
	proof, err := SignPeerRequest(peerPriv, "GET", "/v1/peer/cache/"+grant.SHA256, grant.Nonce, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPeerRequest(grant.TargetPubKey, proof, "GET", "/v1/peer/cache/"+grant.SHA256, grant.Nonce, now, accept); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	if err := VerifyPeerRequest(grant.TargetPubKey, proof, "GET", "/v1/peer/cache/"+grant.SHA256, grant.Nonce, now, accept); err == nil {
		t.Fatal("replayed proof accepted")
	}
	if err := VerifyPeerRequest(grant.TargetPubKey, proof, "HEAD", "/v1/peer/cache/"+grant.SHA256, grant.Nonce, now, nil); err == nil {
		t.Fatal("method-substituted proof accepted")
	}
}

func TestValidatePrivatePeerAddress(t *testing.T) {
	if err := ValidatePrivatePeerAddress("192.168.1.25:9443", false); err != nil {
		t.Fatalf("RFC1918 IPv4 rejected: %v", err)
	}
	if err := ValidatePrivatePeerAddress("127.0.0.1:9443", false); err == nil {
		t.Fatal("loopback allowed when allowLoopback=false")
	}
	if err := ValidatePrivatePeerAddress("127.0.0.1:9443", true); err != nil {
		t.Fatalf("loopback rejected when allowLoopback=true: %v", err)
	}
	for _, bad := range []string{
		"169.254.169.254:80",
		"8.8.8.8:443",
		"0.0.0.0:8080",
		"224.0.0.1:8080",
		"evil.local:8443",
		"192.168.1.1:0",
	} {
		if err := ValidatePrivatePeerAddress(bad, true); err == nil {
			t.Fatalf("unsafe peer address %q accepted", bad)
		}
	}
}

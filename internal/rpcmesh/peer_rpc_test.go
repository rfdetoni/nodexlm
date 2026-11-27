// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package rpcmesh

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestRPCWireCompatibilityAndDirectTensorCopyFailClosed(t *testing.T) {
	if err := CheckWireCompatibility(10, 0, []string{"directional-v1"}); err != nil {
		t.Fatalf("expected 10.0 directional-v1 compatible: %v", err)
	}
	if err := CheckWireCompatibility(9, 0, nil); !errors.Is(err, ErrRPCWireIncompatible) {
		t.Fatalf("expected ErrRPCWireIncompatible for major 9, got %v", err)
	}
	if err := CheckWireCompatibility(11, 0, nil); !errors.Is(err, ErrRPCWireIncompatible) {
		t.Fatalf("expected ErrRPCWireIncompatible for major 11, got %v", err)
	}
	if err := CheckWireCompatibility(10, 0, []string{"direct-tensor-copy-v1"}); !errors.Is(err, ErrDirectTensorCopyUnsupported) {
		t.Fatalf("expected ErrDirectTensorCopyUnsupported, got %v", err)
	}
	if err := CheckWireCompatibility(10, 0, []string{"unknown-required-cap"}); !errors.Is(err, ErrRPCWireIncompatible) {
		t.Fatalf("expected ErrRPCWireIncompatible for unknown cap, got %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, peerPub, err := protocol.GeneratePeerKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	grant, err := protocol.SignPeerGrant(priv, protocol.PeerGrant{
		ClusterID:    "c1",
		Epoch:        1,
		SourceNodeID: "guest-a",
		TargetPeerID: "guest-b",
		TargetPubKey: peerPub,
		Scope:        "rpc",
		IssuedAt:     now,
		ExpiresAt:    now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = ValidatePeerRPCSession(grant, pub, "c1", 1, "guest-a", LocalPeerRPCCapabilities(), now)
	if !errors.Is(err, ErrDirectTensorCopyUnsupported) {
		t.Fatalf("expected explicit unsupported error on 10.0, got %v", err)
	}
}

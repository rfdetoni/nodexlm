// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package safedownload

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func seedPeerModel(t *testing.T, dir string, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	modelPath := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(modelPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(dir, ".stage")
	meta, _ := json.Marshal(partial{SHA256: digest, Total: int64(len(data))})
	if err := os.WriteFile(stage+".json", meta, 0600); err != nil {
		t.Fatal(err)
	}
	RememberPublished(WithContentCache(t.Context(), dir), stage, modelPath)
	return digest
}

func TestPeerCacheDirectTransferAndSecurityRejection(t *testing.T) {
	t.Setenv("NODEXLM_MODEL_P2P", "1")
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	payload := make([]byte, 5<<20) // > 4 MiB chunk to exercise concurrent range GETs
	copy(payload, []byte("GGUF"))
	for i := 4; i < len(payload); i += 4096 {
		payload[i] = byte(i % 251)
	}
	digest := seedPeerModel(t, srcDir, payload)

	authPub, authPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	peerPriv, peerPubHex, err := protocol.GeneratePeerKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewPeerCacheServer("node-a", srcDir, "cluster-1", authPub, 1, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	now := time.Now().UTC()
	grant, err := protocol.SignPeerGrant(authPriv, protocol.PeerGrant{
		ClusterID:    "cluster-1",
		Epoch:        1,
		SourceNodeID: "node-a",
		TargetPeerID: "node-b",
		TargetPubKey: peerPubHex,
		Scope:        "cache",
		SHA256:       digest,
		Size:         int64(len(payload)),
		IssuedAt:     now,
		ExpiresAt:    now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	src := PeerFetchSource{
		NodeID:     "node-a",
		Address:    srv.Address(),
		CertSHA256: srv.CertSHA256(),
		Size:       int64(len(payload)),
		Grant:      grant,
	}

	dest := filepath.Join(dstDir, "downloaded.gguf")
	n, err := FetchFromPeer(context.Background(), src, "node-b", peerPriv, dest, true, nil)
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("direct peer transfer failed: n=%d err=%v", n, err)
	}
	if srv.BytesSent() < uint64(len(payload)) {
		t.Fatalf("expected direct peer server bytes >= %d, got %d", len(payload), srv.BytesSent())
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got[:4]) != "GGUF" || len(got) != len(payload) {
		t.Fatalf("downloaded model corrupted")
	}

	// Wrong peer TLS certificate (MITM simulation) must fail closed.
	badCert := src
	badCert.CertSHA256 = strings.Repeat("0", 64)
	if _, err := FetchFromPeer(context.Background(), badCert, "node-b", peerPriv, filepath.Join(dstDir, "mitm.gguf"), true, nil); err == nil {
		t.Fatal("MITM wrong certificate accepted")
	}

	// Foreign cluster grant must fail.
	_, foreignPriv, _ := ed25519.GenerateKey(rand.Reader)
	foreignGrant, _ := protocol.SignPeerGrant(foreignPriv, protocol.PeerGrant{
		ClusterID: "cluster-1", Epoch: 1, SourceNodeID: "node-a", TargetPeerID: "node-b",
		TargetPubKey: peerPubHex, Scope: "cache", SHA256: digest, Size: int64(len(payload)),
		IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	})
	badCluster := src
	badCluster.Grant = foreignGrant
	if _, err := FetchFromPeer(context.Background(), badCluster, "node-b", peerPriv, filepath.Join(dstDir, "foreign.gguf"), true, nil); err == nil {
		t.Fatal("foreign authority grant accepted")
	}

	// Token substitution (wrong node identity / wrong key) must fail.
	otherPriv, _, _ := protocol.GeneratePeerKeyPair()
	if _, err := FetchFromPeer(context.Background(), src, "node-c", peerPriv, filepath.Join(dstDir, "sub1.gguf"), true, nil); err == nil {
		t.Fatal("wrong target node accepted")
	}
	if _, err := FetchFromPeer(context.Background(), src, "node-b", otherPriv, filepath.Join(dstDir, "sub2.gguf"), true, nil); err == nil {
		t.Fatal("wrong PoP private key accepted")
	}

	// Revocation must take effect immediately.
	srv.UpdateAuthority(2, []string{"node-b"})
	if _, err := FetchFromPeer(context.Background(), src, "node-b", peerPriv, filepath.Join(dstDir, "revoked.gguf"), true, nil); err == nil {
		t.Fatal("revoked node allowed to download")
	}

	// Unauthenticated raw HTTP/HTTPS access must be denied.
	resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: srv.server.TLSConfig}}).Get("https://" + srv.Address() + "/v1/peer/cache/" + digest)
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 on raw unauthenticated peer request, got %d", resp.StatusCode)
		}
	}
	if !errors.Is(ErrPeerCorrupt, ErrPeerCorrupt) {
		t.Fatal("sentinel error check")
	}
}

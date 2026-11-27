// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

func TestP2PDirectGuestCacheTransferZeroHostPayloadAndHostFallback(t *testing.T) {
	t.Setenv("NODEXLM_MODEL_P2P", "1")
	t.Setenv("NODEXLM_P2P_ALLOW_LOOPBACK", "1")

	hostCacheDir := t.TempDir()
	guestADir := t.TempDir()
	guestBDir := t.TempDir()

	body := make([]byte, 256*1024)
	copy(body, []byte("GGUF"))
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	seed := func(dir string) {
		modelFile := filepath.Join(dir, "tiny.gguf")
		if err := os.WriteFile(modelFile, body, 0600); err != nil {
			t.Fatal(err)
		}
		stage := filepath.Join(dir, ".stage")
		meta := fmt.Sprintf("{\"sha256\":%q,\"total\":%d}", digest, len(body))
		if err := os.WriteFile(stage+".json", []byte(meta), 0600); err != nil {
			t.Fatal(err)
		}
		safedownload.RememberPublished(safedownload.WithContentCache(t.Context(), dir), stage, modelFile)
	}
	seed(hostCacheDir)
	seed(guestADir)

	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetContentCacheDirectory(hostCacheDir); err != nil {
		t.Fatal(err)
	}

	hostPayloadHits := 0
	lan := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/nodes/cache/") &&
			!strings.HasPrefix(r.URL.Path, "/v1/nodes/cache/announce") &&
			!strings.HasPrefix(r.URL.Path, "/v1/nodes/cache/peers/") {
			hostPayloadHits++
		}
		s.LANHandler().ServeHTTP(w, r)
	})

	_, pubHexA, err := protocol.GeneratePeerKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	ecB, pubHexB, err := protocol.GeneratePeerKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	credARec := call(t, lan, http.MethodPost, "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{
		Device: protocol.Device{Name: "guest-a", Platform: "linux", CPUCount: 4, PeerPubKey: pubHexA},
	})
	if credARec.Code != http.StatusCreated {
		t.Fatalf("enroll A: %d %s", credARec.Code, credARec.Body.String())
	}
	var credA protocol.Credential
	if err := json.Unmarshal(credARec.Body.Bytes(), &credA); err != nil {
		t.Fatal(err)
	}
	credBRec := call(t, lan, http.MethodPost, "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{
		Device: protocol.Device{Name: "guest-b", Platform: "android", CPUCount: 8, PeerPubKey: pubHexB},
	})
	if credBRec.Code != http.StatusCreated {
		t.Fatalf("enroll B: %d %s", credBRec.Code, credBRec.Body.String())
	}
	var credB protocol.Credential
	if err := json.Unmarshal(credBRec.Body.Bytes(), &credB); err != nil {
		t.Fatal(err)
	}

	authPubBytes, err := hex.DecodeString(credA.AuthorityPubKey)
	if err != nil {
		t.Fatal(err)
	}
	peerSrv, err := safedownload.NewPeerCacheServer(credA.NodeID, guestADir, credA.ClusterID, ed25519.PublicKey(authPubBytes), credA.Epoch, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peerSrv.Close()

	// Reject SSRF announcement (link-local metadata IP).
	ssrfAnnounce := call(t, lan, http.MethodPost, "/v1/nodes/cache/announce", credA.Token, credA.NodeID, map[string]any{
		"listen_addr": "169.254.169.254:8080",
		"cert_sha256": peerSrv.CertSHA256(),
		"items":       []map[string]any{{"sha256": digest, "size": len(body)}},
	})
	if ssrfAnnounce.Code != http.StatusBadRequest {
		t.Fatalf("expected SSRF announcement rejected with 400, got %d", ssrfAnnounce.Code)
	}

	okAnnounce := call(t, lan, http.MethodPost, "/v1/nodes/cache/announce", credA.Token, credA.NodeID, map[string]any{
		"listen_addr": peerSrv.Address(),
		"cert_sha256": peerSrv.CertSHA256(),
		"items":       []map[string]any{{"sha256": digest, "size": len(body)}},
	})
	if okAnnounce.Code != http.StatusOK {
		t.Fatalf("announce failed: %d %s", okAnnounce.Code, okAnnounce.Body.String())
	}

	// Unauthenticated / non-node access must be rejected.
	unauthLookup := call(t, lan, http.MethodGet, "/v1/nodes/cache/peers/"+digest, "", "", nil)
	if unauthLookup.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated lookup, got %d", unauthLookup.Code)
	}

	lookup := call(t, lan, http.MethodGet, "/v1/nodes/cache/peers/"+digest, credB.Token, credB.NodeID, nil)
	if lookup.Code != http.StatusOK {
		t.Fatalf("lookup failed: %d %s", lookup.Code, lookup.Body.String())
	}
	var lookupResp struct {
		ClusterID string                         `json:"cluster_id"`
		Epoch     uint64                         `json:"epoch"`
		Peers     []safedownload.PeerFetchSource `json:"peers"`
	}
	if err := json.Unmarshal(lookup.Body.Bytes(), &lookupResp); err != nil {
		t.Fatal(err)
	}
	if len(lookupResp.Peers) != 1 || lookupResp.Peers[0].NodeID != credA.NodeID {
		t.Fatalf("unexpected peers: %+v", lookupResp.Peers)
	}

	dest := filepath.Join(guestBDir, "tiny.gguf")
	if _, err := safedownload.FetchFromPeer(context.Background(), lookupResp.Peers[0], credB.NodeID, ecB, dest, true, nil); err != nil {
		t.Fatalf("direct Guest A -> Guest B fetch failed: %v", err)
	}
	if hostPayloadHits != 0 {
		t.Fatalf("expected 0 Host payload hits during direct peer transfer, got %d", hostPayloadHits)
	}
	if peerSrv.BytesSent() < uint64(len(body)) {
		t.Fatalf("expected direct peer server to send >= %d bytes, got %d", len(body), peerSrv.BytesSent())
	}

	// Revoking Guest A removes it from future lookups immediately.
	revoke := call(t, s.Handler(), http.MethodDelete, "/v1/nodes/"+credA.NodeID, adminSecret, "", nil)
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke failed: %d", revoke.Code)
	}
	lookupAfterRevoke := call(t, lan, http.MethodGet, "/v1/nodes/cache/peers/"+digest, credB.Token, credB.NodeID, nil)
	var after struct {
		Peers []safedownload.PeerFetchSource `json:"peers"`
	}
	_ = json.Unmarshal(lookupAfterRevoke.Body.Bytes(), &after)
	if len(after.Peers) != 0 {
		t.Fatalf("revoked peer still advertised: %+v", after.Peers)
	}

	// Fallback to Host authenticated cache still works when no peers remain.
	hostFetch := call(t, lan, http.MethodGet, "/v1/nodes/cache/"+digest, credB.Token, credB.NodeID, nil)
	if hostFetch.Code != http.StatusOK || hostPayloadHits != 1 {
		t.Fatalf("Host cache fallback failed: code=%d hits=%d", hostFetch.Code, hostPayloadHits)
	}
}

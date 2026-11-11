// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"github.com/rfdetoni/nodexlm/internal/pairing"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"math/big"
	"net/http"
	"testing"
	"time"
)

func TestPairingEnrollAndRenew(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}, &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PreparePairing(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})); err != nil {
		t.Fatal(err)
	}
	code, err := s.StartPairing(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	d := protocol.Device{Name: "machine", Platform: "linux", CPUCount: 8}
	bad := call(t, h, http.MethodPost, "/v1/pairing/enroll", "", "", map[string]any{"code": "AAAAAAAA", "device": d})
	if bad.Code != 401 && code != "AAAAAAAA" {
		t.Fatalf("wrong code: %d", bad.Code)
	}
	good := call(t, h, http.MethodPost, "/v1/pairing/enroll", "", "", map[string]any{"code": code, "device": d})
	if good.Code != 201 {
		t.Fatalf("correct code: %d %s", good.Code, good.Body.String())
	}
	var cred protocol.Credential
	if err := json.Unmarshal(good.Body.Bytes(), &cred); err != nil {
		t.Fatal(err)
	}
	if cred.Token == code || len(cred.Token) < 32 {
		t.Fatal("expected separate long-lived credential")
	}
	reset := call(t, h, "POST", "/v1/pairing/start", joinSecret, "", map[string]string{})
	if reset.Code != 401 {
		t.Fatal("enrollment credential authorized admin-only rotation")
	}
	reset = call(t, h, "POST", "/v1/pairing/start", adminSecret, "", map[string]string{})
	if reset.Code != 200 {
		t.Fatalf("admin rotation failed: %s", reset.Body.String())
	}
	var rotated struct {
		Code        string    `json:"code"`
		ExpiresIn   string    `json:"expires_in"`
		ExpiresInMS int64     `json:"expires_in_ms"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(reset.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if !pairing.ValidCode(rotated.Code) || rotated.Code == code || rotated.ExpiresIn != "10m" ||
		rotated.ExpiresInMS < 598000 || rotated.ExpiresInMS > 600000 ||
		time.Until(rotated.ExpiresAt) < 9*time.Minute || time.Until(rotated.ExpiresAt) > 10*time.Minute {
		t.Fatalf("invalid pairing expiration metadata: %+v", rotated)
	}
	if reset.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("pairing credentials must not be cached")
	}
	stale := call(t, h, "POST", "/v1/pairing/enroll", "", "", map[string]any{"code": code, "device": d})
	if stale.Code != 401 {
		t.Fatal("old pairing code still valid after rotation")
	}
}

func TestRevocationInvalidatesNode(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	created := call(t, h, "POST", "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{Device: protocol.Device{Name: "revoked-node", Platform: "linux", CPUCount: 1}})
	if created.Code != 201 {
		t.Fatalf("enroll: %s", created.Body.String())
	}
	var cred protocol.Credential
	if err := json.Unmarshal(created.Body.Bytes(), &cred); err != nil {
		t.Fatal(err)
	}
	unauthorized := call(t, h, "DELETE", "/v1/nodes/"+cred.NodeID, joinSecret, "", nil)
	if unauthorized.Code != 401 {
		t.Fatal("join token authorized deletion")
	}
	revoked := call(t, h, "DELETE", "/v1/nodes/"+cred.NodeID, adminSecret, "", nil)
	if revoked.Code != 200 {
		t.Fatalf("revoke: %s", revoked.Body.String())
	}
	lease := call(t, h, "POST", "/v1/nodes/lease", cred.Token, cred.NodeID, map[string]any{"models": []string{"test"}})
	if lease.Code != 401 {
		t.Fatalf("revoked node still works: %d", lease.Code)
	}
}

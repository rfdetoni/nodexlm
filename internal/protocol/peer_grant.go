// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package protocol

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	MaxPeerGrantTTL = 5 * time.Minute
	MaxPeerProofAge = 30 * time.Second
)

// PeerGrant is a Coordinator-signed, short-lived, epoch- and scope-bound
// authorization allowing TargetPeerID to access a single capability on SourceNodeID.
type PeerGrant struct {
	Version      int       `json:"version"`
	ClusterID    string    `json:"cluster_id"`
	Epoch        uint64    `json:"epoch"`
	Issuer       string    `json:"issuer"`
	SourceNodeID string    `json:"source_node_id"`
	TargetPeerID string    `json:"target_peer_id"`
	TargetPubKey string    `json:"target_pub_key"`
	Scope        string    `json:"scope"`
	SHA256       string    `json:"sha256,omitempty"`
	Size         int64     `json:"size,omitempty"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Nonce        string    `json:"nonce"`
	Signature    string    `json:"signature,omitempty"`
}

func (g PeerGrant) canonicalBytes() ([]byte, error) {
	unsigned := g
	unsigned.Signature = ""
	unsigned.IssuedAt = unsigned.IssuedAt.UTC().Truncate(time.Millisecond)
	unsigned.ExpiresAt = unsigned.ExpiresAt.UTC().Truncate(time.Millisecond)
	return json.Marshal(unsigned)
}

func SignPeerGrant(priv ed25519.PrivateKey, g PeerGrant) (PeerGrant, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return PeerGrant{}, errors.New("invalid coordinator signing key")
	}
	if g.Version == 0 {
		g.Version = 1
	}
	if g.Issuer == "" {
		g.Issuer = "coordinator"
	}
	if g.Nonce == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return PeerGrant{}, err
		}
		g.Nonce = hex.EncodeToString(b[:])
	}
	g.IssuedAt = g.IssuedAt.UTC().Truncate(time.Millisecond)
	g.ExpiresAt = g.ExpiresAt.UTC().Truncate(time.Millisecond)
	if err := ValidatePeerGrantFields(g); err != nil {
		return PeerGrant{}, err
	}
	payload, err := g.canonicalBytes()
	if err != nil {
		return PeerGrant{}, err
	}
	g.Signature = hex.EncodeToString(ed25519.Sign(priv, payload))
	return g, nil
}

func ValidatePeerGrantFields(g PeerGrant) error {
	if g.Version != 1 || g.ClusterID == "" || g.Epoch == 0 || g.Issuer != "coordinator" ||
		g.SourceNodeID == "" || g.TargetPeerID == "" || g.SourceNodeID == g.TargetPeerID ||
		!ValidPeerPubKey(g.TargetPubKey) || len(g.Nonce) != 32 {
		return errors.New("invalid peer grant identity or bounds")
	}
	if _, err := hex.DecodeString(g.Nonce); err != nil {
		return errors.New("invalid peer grant nonce")
	}
	if !g.ExpiresAt.After(g.IssuedAt) || g.ExpiresAt.Sub(g.IssuedAt) > MaxPeerGrantTTL {
		return errors.New("invalid peer grant lifetime")
	}
	switch g.Scope {
	case "cache":
		if !IsSHA256Hex(g.SHA256) || g.Size < 8 || g.Size > 150<<30 {
			return errors.New("invalid cache peer grant scope")
		}
	case "rpc":
		// RPC peer grant binds source/target/epoch/cluster.
	default:
		return errors.New("unsupported peer grant scope")
	}
	return nil
}

func VerifyPeerGrant(g PeerGrant, pub ed25519.PublicKey, expectedCluster string, minEpoch uint64, expectedSource, expectedScope string, now time.Time) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("missing coordinator authority public key")
	}
	if err := ValidatePeerGrantFields(g); err != nil {
		return err
	}
	sig, err := hex.DecodeString(g.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("invalid peer grant signature encoding")
	}
	payload, err := g.canonicalBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, payload, sig) {
		return errors.New("peer grant signature mismatch")
	}
	if expectedCluster != "" && g.ClusterID != expectedCluster {
		return errors.New("peer grant cluster mismatch")
	}
	if g.Epoch < minEpoch {
		return errors.New("peer grant epoch expired")
	}
	if expectedSource != "" && g.SourceNodeID != expectedSource {
		return errors.New("peer grant source node mismatch")
	}
	if expectedScope != "" && g.Scope != expectedScope {
		return errors.New("peer grant scope mismatch")
	}
	if now.Before(g.IssuedAt.Add(-30*time.Second)) || !now.Before(g.ExpiresAt) {
		return errors.New("peer grant expired or not yet valid")
	}
	return nil
}

func IsSHA256Hex(v string) bool {
	if len(v) != 64 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// ValidPeerPubKey validates an ECDSA P-256 SPKI DER public key encoded as lowercase hex.
func ValidPeerPubKey(hexKey string) bool {
	if len(hexKey) == 0 || len(hexKey) > 256 {
		return false
	}
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return false
	}
	pub, err := x509.ParsePKIXPublicKey(raw)
	if err != nil {
		return false
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	return ok && ec.Curve == elliptic.P256()
}

func GeneratePeerKeyPair() (*ecdsa.PrivateKey, string, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, "", err
	}
	return priv, hex.EncodeToString(der), nil
}

func SignPeerRequest(priv *ecdsa.PrivateKey, method, path, grantNonce string, now time.Time) (string, error) {
	if priv == nil {
		return "", errors.New("missing peer private key")
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	ts := strconv.FormatInt(now.UTC().UnixMilli(), 10)
	reqNonce := hex.EncodeToString(nonce[:])
	digest := sha256.Sum256([]byte(method + "\n" + path + "\n" + ts + "\n" + reqNonce + "\n" + grantNonce))
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest[:])
	if err != nil {
		return "", err
	}
	return ts + "." + reqNonce + "." + hex.EncodeToString(sig), nil
}

func VerifyPeerRequest(pubHex, proof, method, path, grantNonce string, now time.Time, acceptNonce func(string, time.Time) bool) error {
	parts := strings.Split(proof, ".")
	if len(parts) != 3 || len(parts[1]) != 24 {
		return errors.New("invalid peer proof format")
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return errors.New("invalid peer proof nonce")
	}
	ms, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return errors.New("invalid peer proof timestamp")
	}
	ts := time.UnixMilli(ms)
	if now.Sub(ts) > MaxPeerProofAge || ts.Sub(now) > MaxPeerProofAge {
		return errors.New("peer proof timestamp outside window")
	}
	rawPub, err := hex.DecodeString(pubHex)
	if err != nil {
		return errors.New("invalid peer public key")
	}
	parsed, err := x509.ParsePKIXPublicKey(rawPub)
	if err != nil {
		return errors.New("invalid peer public key")
	}
	ecPub, ok := parsed.(*ecdsa.PublicKey)
	if !ok || ecPub.Curve != elliptic.P256() {
		return errors.New("unsupported peer public key curve")
	}
	sig, err := hex.DecodeString(parts[2])
	if err != nil || len(sig) == 0 || len(sig) > 128 {
		return errors.New("invalid peer proof signature")
	}
	digest := sha256.Sum256([]byte(method + "\n" + path + "\n" + parts[0] + "\n" + parts[1] + "\n" + grantNonce))
	if !ecdsa.VerifyASN1(ecPub, digest[:], sig) {
		return errors.New("peer proof signature verification failed")
	}
	if acceptNonce != nil && !acceptNonce(parts[1], ts.Add(2*MaxPeerProofAge)) {
		return errors.New("replayed peer request nonce")
	}
	return nil
}

// ValidatePrivatePeerAddress strictly rejects hostnames (DNS rebinding), public
// IPs, link-local metadata ranges, multicast, and invalid ports. Loopback is
// rejected unless allowLoopback is explicitly enabled (e.g., local test harnesses).
func ValidatePrivatePeerAddress(addr string, allowLoopback bool) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid peer address: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid peer port")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return errors.New("peer address must be an IP literal")
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return errors.New("peer address uses forbidden special IP range")
	}
	if ip.IsLoopback() {
		if !allowLoopback {
			return errors.New("loopback peer address disallowed")
		}
		return nil
	}
	if !ip.IsPrivate() {
		return errors.New("peer address must be private LAN IP")
	}
	return nil
}

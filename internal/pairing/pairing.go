// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package pairing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	Alphabet         = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	DiscoveryPort    = 46465
	DiscoveryMessage = "NODEXLM_DISCOVER_V1"
	// An intentionally expensive KDF makes intercepted LAN discovery records
	// costly to brute force offline. This is NOT a general-purpose PAKE.
	kdfRounds = 200000
)

// Offer contains public data only; the eight-character code is never advertised.
// Proof binds knowledge of the temporary code to the coordinator TLS identity.
type Offer struct {
	Endpoint string    `json:"endpoint"`
	Salt     string    `json:"salt"`
	Nonce    string    `json:"nonce"`
	Proof    string    `json:"proof"`
	Expires  time.Time `json:"expires"`
}

type Session struct {
	code        string
	salt        [16]byte
	nonce       [16]byte
	proof       [32]byte
	fingerprint [32]byte
	expires     time.Time
	failures    int
	perIP       map[string]int
	joined      int
}

func GenerateCode() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// 32-symbol alphabet: uniform byte masking, without modulo bias.
	for i := range b {
		b[i] = Alphabet[int(b[i])&31]
	}
	return string(b[:]), nil
}

func ValidCode(code string) bool {
	if len(code) != 8 {
		return false
	}
	for _, ch := range code {
		if !strings.ContainsRune(Alphabet, ch) {
			return false
		}
	}
	return true
}

func Normalize(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(code), "-", ""), " ", ""))
}

// pbkdf2SHA256 implements PBKDF2-HMAC-SHA256 for one 32-byte output block.
// Kept self-contained for the zero-dependency Go runtime.
func pbkdf2SHA256(password string, salt []byte) [32]byte {
	var block [4]byte
	block[3] = 1
	h := hmac.New(sha256.New, []byte(password))
	_, _ = h.Write(salt)
	_, _ = h.Write(block[:])
	u := h.Sum(nil)
	var out [32]byte
	copy(out[:], u)
	for i := 1; i < kdfRounds; i++ {
		h.Reset()
		_, _ = h.Write(u)
		u = h.Sum(u[:0])
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}

func commitment(code string, salt, nonce []byte, fingerprint [32]byte) [32]byte {
	key := pbkdf2SHA256(code, salt)
	h := hmac.New(sha256.New, key[:])
	_, _ = h.Write([]byte("nodexlm-pairing-v1\x00"))
	_, _ = h.Write(fingerprint[:])
	_, _ = h.Write(nonce)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func NewSession(certDER []byte, ttl time.Duration) (*Session, string, error) {
	if len(certDER) == 0 || ttl <= 0 || ttl > 30*time.Minute {
		return nil, "", errors.New("invalid pairing certificate or TTL")
	}
	code, err := GenerateCode()
	if err != nil {
		return nil, "", err
	}
	p := &Session{expires: time.Now().Add(ttl), code: code, perIP: make(map[string]int), fingerprint: sha256.Sum256(certDER)}
	if _, err := rand.Read(p.salt[:]); err != nil {
		return nil, "", err
	}
	if _, err := rand.Read(p.nonce[:]); err != nil {
		return nil, "", err
	}
	p.proof = commitment(code, p.salt[:], p.nonce[:], p.fingerprint)
	return p, code, nil
}
func (p *Session) Active() bool {
	return p != nil && time.Now().Before(p.expires) && p.failures < 40 && p.joined < 16
}
func (p *Session) Offer(endpoint string) Offer {
	return Offer{Endpoint: endpoint, Salt: base64.RawURLEncoding.EncodeToString(p.salt[:]), Nonce: base64.RawURLEncoding.EncodeToString(p.nonce[:]), Proof: hex.EncodeToString(p.proof[:]), Expires: p.expires}
}

// Attempt must be called under the coordinator lock. Throttles by source IP
// and pairing session; a code never becomes a long-lived bearer credential.
func (p *Session) Attempt(code, ip string) bool {
	if !p.Active() || p.perIP[ip] >= 5 {
		return false
	}
	candidate := sha256.Sum256([]byte(code))
	expected := sha256.Sum256([]byte(p.code))
	if !ValidCode(code) || subtle.ConstantTimeCompare(candidate[:], expected[:]) != 1 {
		p.failures++
		p.perIP[ip]++
		return false
	}
	p.joined++
	return true
}

func VerifyOffer(code string, offer Offer, certDER []byte) error {
	code = Normalize(code)
	if !ValidCode(code) {
		return errors.New("pairing code must contain eight alphanumeric characters")
	}
	if time.Now().After(offer.Expires) || time.Until(offer.Expires) > 31*time.Minute {
		return errors.New("pairing offer expired or invalid")
	}
	u, err := url.Parse(offer.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid discovery endpoint")
	}
	salt, err := base64.RawURLEncoding.DecodeString(offer.Salt)
	if err != nil || len(salt) != 16 {
		return errors.New("invalid pairing salt")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(offer.Nonce)
	if err != nil || len(nonce) != 16 {
		return errors.New("invalid pairing nonce")
	}
	proof, err := hex.DecodeString(offer.Proof)
	if err != nil || len(proof) != 32 {
		return errors.New("invalid pairing proof")
	}
	expected := commitment(code, salt, nonce, sha256.Sum256(certDER))
	if subtle.ConstantTimeCompare(proof, expected[:]) != 1 {
		return errors.New("pairing code does not match the coordinator TLS identity")
	}
	return nil
}

// FetchUntrustedOffer only fetches PUBLIC metadata and the TLS peer certificate.
// Call VerifyOffer BEFORE sending any secret or enrollment request.
func FetchUntrustedOffer(ctx context.Context, endpoint string) (Offer, []byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return Offer{}, nil, errors.New("HTTPS endpoint required")
	}
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 3 * time.Second},
		Config:    &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}, // #nosec G402 -- no secrets; authenticated by VerifyOffer before use.
	}
	conn, err := dialer.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		return Offer{}, nil, err
	}
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		conn.Close()
		return Offer{}, nil, errors.New("unexpected TLS connection type")
	}
	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		conn.Close()
		return Offer{}, nil, errors.New("server presented no certificate")
	}
	der := append([]byte(nil), certs[0].Raw...)
	conn.Close()
	// Untrusted HTTPS response is used only to retrieve the offer, then verified
	// against the code and authenticated TLS fingerprint before any POST.
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS13,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || subtle.ConstantTimeCompare(sha256sum(cs.PeerCertificates[0].Raw), sha256sum(der)) != 1 {
				return errors.New("TLS certificate changed during discovery")
			}
			return nil
		},
	}}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/pairing/offer", nil)
	if err != nil {
		return Offer{}, nil, err
	}
	resp, err := client.Do(request)
	if err != nil {
		return Offer{}, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Offer{}, nil, fmt.Errorf("pairing unavailable: HTTP %d", resp.StatusCode)
	}
	// The untrusted LAN peer must not send an unbounded JSON response.
	body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<10)+1))
	if err != nil {
		return Offer{}, nil, err
	}
	if len(body) > 8<<10 {
		return Offer{}, nil, errors.New("pairing offer too large")
	}
	var offer Offer
	if err := json.Unmarshal(body, &offer); err != nil {
		return Offer{}, nil, err
	}
	if offer.Endpoint != endpoint {
		return Offer{}, nil, errors.New("discovered endpoint does not match offer")
	}
	return offer, der, nil
}

func sha256sum(der []byte) []byte { sum := sha256.Sum256(der); return sum[:] }

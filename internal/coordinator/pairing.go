// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"time"

	"github.com/rfdetoni/nodexlm/internal/pairing"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// PreparePairing sets the coordinator TLS identity for enrollment.
func (s *Server) PreparePairing(certPEM []byte) error {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("invalid coordinator certificate")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.certDER = append([]byte(nil), block.Bytes...)
	return nil
}

func (s *Server) StartPairing(ttl time.Duration) (string, error) {
	code, _, err := s.startPairing(ttl)
	return code, err
}

// Pairing session and expiration are created together under one lock so the
// timer returned to the console always belongs to the generated code.
func (s *Server) startPairing(ttl time.Duration) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.certDER) == 0 {
		return "", time.Time{}, errors.New("coordinator TLS certificate not configured")
	}
	p, code, err := pairing.NewSession(s.certDER, ttl)
	if err != nil {
		return "", time.Time{}, err
	}
	s.pair = p
	return code, p.Offer("").Expires, nil
}

func (s *Server) Offers(endpoints []string) []pairing.Offer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pair.Active() {
		return nil
	}
	out := make([]pairing.Offer, 0, len(endpoints))
	for _, e := range endpoints {
		out = append(out, s.pair.Offer(e))
	}
	return out
}

func (s *Server) pairingOffer(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pair.Active() {
		fail(w, 404, "pairing not active")
		return
	}
	// Endpoint is generated from the actual HTTPS request host; a client must
	// verify it against the bound certificate and typed code before enrollment.
	respond(w, 200, s.pair.Offer("https://"+r.Host))
}

func (s *Server) pairingStart(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	code, expiresAt, err := s.startPairing(10 * time.Minute)
	if err != nil {
		fail(w, 500, "could not start pairing")
		return
	}
	// expires_in remains for older clients; the millisecond TTL avoids
	// relying on the operator's browser clock being in sync with the Host.
	remaining := max(int64(0), time.Until(expiresAt).Milliseconds())
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, map[string]any{
		"code":          code,
		"expires_in":    "10m",
		"expires_in_ms": remaining,
		"expires_at":    expiresAt.UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) pairingEnroll(w http.ResponseWriter, r *http.Request) {
	ip := pairing.SourceIP(r.RemoteAddr)
	if !pairing.SameLAN(ip) {
		fail(w, 403, "pairing requires a local network connection")
		return
	}
	var req struct {
		Code   string          `json:"code"`
		Device protocol.Device `json:"device"`
	}
	if !parse(w, r, &req) {
		return
	}
	if !validDevice(req.Device) {
		fail(w, 400, "invalid node profile")
		return
	}
	s.mu.Lock()
	permitted := s.pair.Attempt(pairing.Normalize(req.Code), ip.String())
	s.mu.Unlock()
	if !permitted {
		fail(w, 401, "invalid, expired, or rate-limited pairing code")
		return
	}
	s.register(w, req.Device)
}

// revokeNode invalidates a per-node credential without rotating other nodes.
func (s *Server) revokeNode(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.nodes[id]
	if n == nil {
		fail(w, 404, "node not found")
		return
	}
	delete(s.nodes, id)
	select {
	case n.signal <- struct{}{}:
	default:
	}
	if err := s.saveLocked(); err != nil {
		s.nodes[id] = n
		fail(w, 500, "could not persist node revocation")
		return
	}
	if n.active != "" {
		if j := s.jobs[n.active]; j != nil && j.Status == "running" {
			now := time.Now()
			j.Completed = &now
			j.Status = "failed"
			j.Error = "node authorization revoked"
			s.notifyLocked(j.ID)
		}
	}
	s.rpcHub.Remove(id)
	respond(w, 200, map[string]string{"status": "revoked"})
}

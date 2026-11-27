// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const maxRevokedRecord = 1024

type persistedAuthority struct {
	ClusterID     string            `json:"cluster_id"`
	Epoch         uint64            `json:"epoch"`
	PrivateKeyHex string            `json:"private_key_hex"`
	RevokedNodes  map[string]uint64 `json:"revoked_nodes,omitempty"`
}

type AuthoritySnapshot struct {
	ClusterID       string   `json:"cluster_id"`
	Epoch           uint64   `json:"epoch"`
	AuthorityPubKey string   `json:"authority_pubkey"`
	RevokedNodes    []string `json:"revoked_nodes,omitempty"`
}

func newAuthorityIdentity() (clusterID string, epoch uint64, pub ed25519.PublicKey, priv ed25519.PrivateKey, err error) {
	id, err := token()
	if err != nil {
		return "", 0, nil, nil, err
	}
	pub, priv, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", 0, nil, nil, err
	}
	return id[:32], 1, pub, priv, nil
}

func (s *Server) authorityPathLocked() string {
	if s.storePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.storePath), "cluster.authority.json")
}

func (s *Server) loadAuthorityLocked() error {
	path := s.authorityPathLocked()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s.saveAuthorityLocked()
	}
	if err != nil {
		return err
	}
	var meta persistedAuthority
	if err := json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	privBytes, err := hex.DecodeString(meta.PrivateKeyHex)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize || meta.ClusterID == "" || meta.Epoch == 0 {
		return errors.New("invalid persisted cluster authority")
	}
	priv := ed25519.PrivateKey(privBytes)
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid authority public key")
	}
	s.clusterID = meta.ClusterID
	s.epoch = meta.Epoch
	s.authorityPriv = priv
	s.authorityPub = pub
	s.revokedNodes = make(map[string]uint64, len(meta.RevokedNodes))
	for id, ep := range meta.RevokedNodes {
		if id != "" && len(s.revokedNodes) < maxRevokedRecord {
			s.revokedNodes[id] = ep
		}
	}
	return nil
}

func (s *Server) saveAuthorityLocked() error {
	path := s.authorityPathLocked()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	meta := persistedAuthority{
		ClusterID:     s.clusterID,
		Epoch:         s.epoch,
		PrivateKeyHex: hex.EncodeToString(s.authorityPriv),
		RevokedNodes:  s.revokedNodes,
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".authority-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s *Server) recordRevocationLocked(id string) {
	if s.revokedNodes == nil {
		s.revokedNodes = make(map[string]uint64)
	}
	if len(s.revokedNodes) >= maxRevokedRecord {
		var oldestID string
		var oldestEpoch uint64 = ^uint64(0)
		for k, v := range s.revokedNodes {
			if v < oldestEpoch {
				oldestEpoch = v
				oldestID = k
			}
		}
		if oldestID != "" {
			delete(s.revokedNodes, oldestID)
		}
	}
	s.revokedNodes[id] = s.epoch
	_ = time.Now()
}

func (s *Server) authoritySnapshotLocked() AuthoritySnapshot {
	revoked := make([]string, 0, len(s.revokedNodes))
	for id := range s.revokedNodes {
		revoked = append(revoked, id)
	}
	return AuthoritySnapshot{
		ClusterID:       s.clusterID,
		Epoch:           s.epoch,
		AuthorityPubKey: hex.EncodeToString(s.authorityPub),
		RevokedNodes:    revoked,
	}
}

func (s *Server) AuthoritySnapshot() AuthoritySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authoritySnapshotLocked()
}

func (s *Server) SetAllowLoopbackPeers(allow bool) {
	s.mu.Lock()
	s.allowLoopbackPeers = allow
	s.mu.Unlock()
}

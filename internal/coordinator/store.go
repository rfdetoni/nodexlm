// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

type persistedNode struct {
	Device         protocol.Device `json:"device"`
	CredentialHash string          `json:"credential_hash"`
}

// SetStore loads opaque node hashes, never raw node bearer credentials.
// Call once before exposing Handler to concurrent requests.
func (s *Server) SetStore(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storePath = path
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var records []persistedNode
	if err := json.Unmarshal(raw, &records); err != nil {
		return err
	}
	for _, record := range records {
		bytes, err := hex.DecodeString(record.CredentialHash)
		if err != nil || len(bytes) != 32 || record.Device.ID == "" {
			return errors.New("invalid persisted registry")
		}
		var hash [32]byte
		copy(hash[:], bytes)
		record.Device.Status = "offline"
		record.Device.Busy = false
		record.Device.LastSeen = time.Time{}
		s.nodes[record.Device.ID] = &nodeState{device: record.Device, credential: hash, signal: make(chan struct{}, 1)}
	}
	return nil
}

func (s *Server) saveLocked() error {
	if s.storePath == "" {
		return nil
	}
	out := make([]persistedNode, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, persistedNode{Device: n.device, CredentialHash: hex.EncodeToString(n.credential[:])})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.storePath), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.storePath), ".registry-")
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
	return os.Rename(f.Name(), s.storePath)
}

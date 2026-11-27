// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MaxSessionPayloadBytes = 256 << 20 // 256 MiB ceiling for slot state snapshots
	DefaultSessionTTL      = 30 * time.Minute
)

var (
	// ErrLiveMigrationUnsupported explicitly refuses live mid-stream request or
	// tool-call migration when checkpoint continuity cannot be guaranteed.
	ErrLiveMigrationUnsupported = errors.New("live mid-stream session/tool migration unsupported; finish or cancel stream and recover between requests")
)

type SessionManifest struct {
	Version        int       `json:"version"`
	SessionID      string    `json:"session_id"`
	GGUFSHA256     string    `json:"gguf_sha256"`
	RuntimeVersion string    `json:"runtime_version"`
	Architecture   string    `json:"architecture"`
	KVType         string    `json:"kv_type"`
	ContextSize    int       `json:"context_size"`
	SlotFormat     string    `json:"slot_format"`
	PayloadSHA256  string    `json:"payload_sha256"`
	PayloadBytes   int64     `json:"payload_bytes"`
	CreatedAt      time.Time `json:"created_at"`
	TTLSeconds     int64     `json:"ttl_seconds"`
}

type SessionRecoveryPlan struct {
	Mode   string `json:"mode"` // "kv-restore" or "re-prefill"
	Reason string `json:"reason"`
}

func ValidateSessionManifest(m SessionManifest, now time.Time) error {
	if m.Version != 1 || m.SessionID == "" || strings.ContainsAny(m.SessionID, "/\\:..") ||
		len(m.SessionID) > 64 || len(m.GGUFSHA256) != 64 || len(m.PayloadSHA256) != 64 ||
		m.RuntimeVersion == "" || m.Architecture == "" || m.KVType == "" ||
		m.ContextSize <= 0 || m.SlotFormat == "" ||
		m.PayloadBytes <= 0 || m.PayloadBytes > MaxSessionPayloadBytes ||
		m.TTLSeconds <= 0 {
		return errors.New("invalid session snapshot manifest")
	}
	if _, err := hex.DecodeString(m.GGUFSHA256); err != nil {
		return errors.New("invalid session GGUF hash")
	}
	if _, err := hex.DecodeString(m.PayloadSHA256); err != nil {
		return errors.New("invalid session payload hash")
	}
	if now.After(m.CreatedAt.Add(time.Duration(m.TTLSeconds) * time.Second)) {
		return errors.New("session snapshot TTL expired")
	}
	return nil
}

// PlanSessionRecovery decides between slot KV restore and explicit re-prefill.
// Never reports live KV restore when architecture, KV type, context, or runtime diverge.
func PlanSessionRecovery(source, target SessionManifest, slotSaveSupported bool, now time.Time) SessionRecoveryPlan {
	if !slotSaveSupported {
		return SessionRecoveryPlan{Mode: "re-prefill", Reason: "runtime slot save/restore unavailable; re-prefill required"}
	}
	if err := ValidateSessionManifest(source, now); err != nil {
		return SessionRecoveryPlan{Mode: "re-prefill", Reason: err.Error()}
	}
	switch {
	case source.GGUFSHA256 != target.GGUFSHA256:
		return SessionRecoveryPlan{Mode: "re-prefill", Reason: "GGUF SHA-256 mismatch"}
	case source.RuntimeVersion != target.RuntimeVersion:
		return SessionRecoveryPlan{Mode: "re-prefill", Reason: "runtime version mismatch"}
	case source.Architecture != target.Architecture:
		return SessionRecoveryPlan{Mode: "re-prefill", Reason: "model architecture mismatch"}
	case source.KVType != target.KVType:
		return SessionRecoveryPlan{Mode: "re-prefill", Reason: "KV cache quantization type mismatch"}
	case source.ContextSize != target.ContextSize:
		return SessionRecoveryPlan{Mode: "re-prefill", Reason: "context size mismatch"}
	case source.SlotFormat != target.SlotFormat:
		return SessionRecoveryPlan{Mode: "re-prefill", Reason: "slot serialization format mismatch"}
	default:
		return SessionRecoveryPlan{Mode: "kv-restore", Reason: "verified matching slot manifest"}
	}
}

func ValidateLiveMigration(inFlightStream bool, pendingToolCalls int) error {
	if inFlightStream || pendingToolCalls > 0 {
		return ErrLiveMigrationUnsupported
	}
	return nil
}

// SaveSessionSnapshot stores a bounded KV/slot snapshot atomically with mode 0600
// and SHA-256 verification. Rejects symlinks and oversized buffers.
func SaveSessionSnapshot(dir string, m SessionManifest, payload []byte) (SessionManifest, error) {
	if len(payload) == 0 || int64(len(payload)) > MaxSessionPayloadBytes {
		return SessionManifest{}, errors.New("session payload empty or exceeds size limit")
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	if m.TTLSeconds <= 0 {
		m.TTLSeconds = int64(DefaultSessionTTL.Seconds())
	}
	m.Version = 1
	sum := sha256.Sum256(payload)
	m.PayloadSHA256 = hex.EncodeToString(sum[:])
	m.PayloadBytes = int64(len(payload))
	if err := ValidateSessionManifest(m, m.CreatedAt); err != nil {
		return SessionManifest{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return SessionManifest{}, err
	}
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return SessionManifest{}, errors.New("unsafe session snapshot directory")
	}
	binPath := filepath.Join(dir, m.SessionID+".slot.bin")
	metaPath := filepath.Join(dir, m.SessionID+".slot.json")
	if err := writePrivateAtomic(binPath, payload); err != nil {
		return SessionManifest{}, err
	}
	rawMeta, err := json.Marshal(m)
	if err != nil {
		_ = os.Remove(binPath)
		return SessionManifest{}, err
	}
	if err := writePrivateAtomic(metaPath, rawMeta); err != nil {
		_ = os.Remove(binPath)
		return SessionManifest{}, err
	}
	return m, nil
}

func LoadSessionSnapshot(dir, sessionID string, expected SessionManifest, now time.Time) (SessionManifest, []byte, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, "/\\:..") || len(sessionID) > 64 {
		return SessionManifest{}, nil, errors.New("invalid session id")
	}
	metaPath := filepath.Join(dir, sessionID+".slot.json")
	binPath := filepath.Join(dir, sessionID+".slot.bin")
	metaInfo, err := os.Lstat(metaPath)
	if err != nil || !metaInfo.Mode().IsRegular() || metaInfo.Size() > 16<<10 {
		return SessionManifest{}, nil, errors.New("invalid or missing session manifest")
	}
	rawMeta, err := os.ReadFile(metaPath)
	if err != nil {
		return SessionManifest{}, nil, err
	}
	var m SessionManifest
	if err := json.Unmarshal(rawMeta, &m); err != nil {
		return SessionManifest{}, nil, err
	}
	if m.SessionID != sessionID {
		return SessionManifest{}, nil, errors.New("session manifest ID mismatch")
	}
	if err := ValidateSessionManifest(m, now); err != nil {
		_ = RemoveSessionSnapshot(dir, sessionID)
		return SessionManifest{}, nil, err
	}
	plan := PlanSessionRecovery(m, expected, true, now)
	if plan.Mode != "kv-restore" {
		return SessionManifest{}, nil, fmt.Errorf("session incompatible for KV restore: %s", plan.Reason)
	}
	binInfo, err := os.Lstat(binPath)
	if err != nil || !binInfo.Mode().IsRegular() || binInfo.Size() != m.PayloadBytes {
		return SessionManifest{}, nil, errors.New("session payload missing or wrong size")
	}
	f, err := os.Open(binPath)
	if err != nil {
		return SessionManifest{}, nil, err
	}
	defer f.Close()
	payload, err := io.ReadAll(io.LimitReader(f, MaxSessionPayloadBytes+1))
	if err != nil || int64(len(payload)) != m.PayloadBytes {
		return SessionManifest{}, nil, errors.New("session payload truncated or oversized")
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != m.PayloadSHA256 {
		return SessionManifest{}, nil, errors.New("session payload checksum mismatch")
	}
	return m, payload, nil
}

func RemoveSessionSnapshot(dir, sessionID string) error {
	if sessionID == "" || strings.ContainsAny(sessionID, "/\\:..") {
		return errors.New("invalid session id")
	}
	_ = os.Remove(filepath.Join(dir, sessionID+".slot.json"))
	_ = os.Remove(filepath.Join(dir, sessionID+".slot.bin"))
	return nil
}

func PruneExpiredSnapshots(dir string, now time.Time) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".slot.json") {
			continue
		}
		sid := strings.TrimSuffix(entry.Name(), ".slot.json")
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			_ = RemoveSessionSnapshot(dir, sid)
			removed++
			continue
		}
		var m SessionManifest
		if json.Unmarshal(raw, &m) != nil || ValidateSessionManifest(m, now) != nil {
			_ = RemoveSessionSnapshot(dir, sid)
			removed++
		}
	}
	return removed
}

func writePrivateAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-slot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
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

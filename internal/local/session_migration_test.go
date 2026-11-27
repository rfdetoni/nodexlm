// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package local

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionSnapshotCompatibilitySecurityAndTTL(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	base := SessionManifest{
		SessionID:      "sess-01",
		GGUFSHA256:     strings.Repeat("a", 64),
		RuntimeVersion: "v1.0.0-r6",
		Architecture:   "qwen2",
		KVType:         "f16",
		ContextSize:    4096,
		SlotFormat:     "llama-slot-v1",
		CreatedAt:      now,
		TTLSeconds:     300,
	}
	payload := []byte("kv-slot-binary-state-sample")
	saved, err := SaveSessionSnapshot(dir, base, payload)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "sess-01.slot.bin")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("expected 0600 private permissions, got err=%v mode=%v", err, info.Mode().Perm())
	}

	gotMeta, gotBytes, err := LoadSessionSnapshot(dir, "sess-01", base, now.Add(time.Minute))
	if err != nil || string(gotBytes) != string(payload) || gotMeta.PayloadSHA256 != saved.PayloadSHA256 {
		t.Fatalf("valid snapshot load failed: err=%v len=%d", err, len(gotBytes))
	}

	// Mismatched KV type or architecture must fall back to re-prefill and fail KV restore.
	mismatch := base
	mismatch.KVType = "q8_0"
	if plan := PlanSessionRecovery(saved, mismatch, true, now); plan.Mode != "re-prefill" {
		t.Fatalf("expected re-prefill on KV mismatch, got %+v", plan)
	}
	if _, _, err := LoadSessionSnapshot(dir, "sess-01", mismatch, now); err == nil {
		t.Fatal("loaded snapshot despite KV quantization mismatch")
	}

	// Without slot save support, always fall back to explicit re-prefill.
	if plan := PlanSessionRecovery(saved, base, false, now); plan.Mode != "re-prefill" {
		t.Fatalf("expected re-prefill when slot save unsupported, got %+v", plan)
	}

	// Tampered payload must be rejected by SHA-256 check.
	if err := os.WriteFile(filepath.Join(dir, "sess-01.slot.bin"), []byte("tampered-payload-same-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadSessionSnapshot(dir, "sess-01", base, now); err == nil {
		t.Fatal("accepted tampered KV payload")
	}

	// Expired snapshot must be rejected and pruned.
	if pruned := PruneExpiredSnapshots(dir, now.Add(10*time.Minute)); pruned != 1 {
		t.Fatalf("expected 1 pruned expired snapshot, got %d", pruned)
	}
	if _, err := os.Stat(filepath.Join(dir, "sess-01.slot.json")); !os.IsNotExist(err) {
		t.Fatalf("expected expired snapshot removed")
	}

	// Live mid-stream / tool-call migration must be explicitly refused.
	if err := ValidateLiveMigration(true, 0); !errors.Is(err, ErrLiveMigrationUnsupported) {
		t.Fatalf("expected ErrLiveMigrationUnsupported for active stream, got %v", err)
	}
	if err := ValidateLiveMigration(false, 1); !errors.Is(err, ErrLiveMigrationUnsupported) {
		t.Fatalf("expected ErrLiveMigrationUnsupported for pending tool call, got %v", err)
	}
	if err := ValidateLiveMigration(false, 0); err != nil {
		t.Fatalf("expected between-request idle migration allowed: %v", err)
	}
}

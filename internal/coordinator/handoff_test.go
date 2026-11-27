// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestHandoffLifecycleFencingRestartAndRollback(t *testing.T) {
	storeDir := t.TempDir()
	storePath := filepath.Join(storeDir, "registry.json")
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetStore(storePath); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	lan := s.LANHandler()

	// Enroll a Linux candidate Guest and an Android Guest.
	linuxEnroll := call(t, lan, http.MethodPost, "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{
		Device: protocol.Device{
			Name: "linux-b", Platform: "linux", CPUCount: 8, RAMBytes: 16 << 30, AvailableRAMBytes: 12 << 30,
			Runtime: protocol.RuntimeCapability{Installed: true, Engine: "llama.cpp", Version: "v1.0.0-r6"},
		},
	})
	var linuxCred protocol.Credential
	if err := json.Unmarshal(linuxEnroll.Body.Bytes(), &linuxCred); err != nil {
		t.Fatal(err)
	}
	androidEnroll := call(t, lan, http.MethodPost, "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{
		Device: protocol.Device{
			Name: "phone-a", Platform: "android", CPUCount: 8, RAMBytes: 12 << 30, AvailableRAMBytes: 8 << 30,
			Runtime: protocol.RuntimeCapability{Installed: true, Engine: "ggml-rpc-cpu", Version: "v1.0.0-r6"},
		},
	})
	var androidCred protocol.Credential
	if err := json.Unmarshal(androidEnroll.Body.Bytes(), &androidCred); err != nil {
		t.Fatal(err)
	}

	certDigest := strings.Repeat("c", 64)
	// Android automatic promotion must be refused unless force_android=true.
	badAndroid := call(t, h, http.MethodPost, "/v1/handoff/plan", adminSecret, "", HandoffPlanRequest{
		TargetNodeID: androidCred.NodeID, Model: "qwen.gguf", ModelBytes: 4 << 30,
		NewHostURL: "https://192.168.1.20:8443", NewHostCertSHA256: certDigest,
	})
	if badAndroid.Code != http.StatusConflict {
		t.Fatalf("expected Android promotion refused, got %d %s", badAndroid.Code, badAndroid.Body.String())
	}

	// LANHandler must never expose handoff endpoints; non-admin token must get 401.
	lanAttempt := call(t, lan, http.MethodGet, "/v1/handoff/status", adminSecret, "", nil)
	if lanAttempt.Code != http.StatusNotFound && lanAttempt.Code != http.StatusUnauthorized {
		t.Fatalf("handoff status exposed on LANHandler: %d", lanAttempt.Code)
	}
	nonAdmin := call(t, h, http.MethodGet, "/v1/handoff/status", joinSecret, "", nil)
	if nonAdmin.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for non-admin handoff status, got %d", nonAdmin.Code)
	}

	// Plan handoff to Linux node B.
	planRec := call(t, h, http.MethodPost, "/v1/handoff/plan", adminSecret, "", HandoffPlanRequest{
		TargetNodeID: linuxCred.NodeID, Model: "qwen.gguf", ModelBytes: 4 << 30,
		NewHostURL: "https://192.168.1.20:8443", NewHostCertSHA256: certDigest,
	})
	if planRec.Code != http.StatusOK {
		t.Fatalf("plan failed: %d %s", planRec.Code, planRec.Body.String())
	}

	// Unconfirmed start must fail; staging failure must move to failed and allow rollback.
	noConfirm := call(t, h, http.MethodPost, "/v1/handoff/start", adminSecret, "", map[string]any{"confirm": false, "staged_verified": true})
	if noConfirm.Code != http.StatusConflict {
		t.Fatalf("expected unconfirmed handoff refused, got %d", noConfirm.Code)
	}
	badStage := call(t, h, http.MethodPost, "/v1/handoff/start", adminSecret, "", map[string]any{"confirm": true, "staged_verified": false})
	if badStage.Code != http.StatusConflict {
		t.Fatalf("expected unverified staging failure, got %d", badStage.Code)
	}
	if s.HandoffStatus().Phase != HandoffFailed {
		t.Fatalf("expected failed phase after unverified stage, got %s", s.HandoffStatus().Phase)
	}
	if rb := call(t, h, http.MethodPost, "/v1/handoff/rollback", adminSecret, "", map[string]any{"new_authority_fenced": false}); rb.Code != http.StatusOK {
		t.Fatalf("pre-fence rollback failed: %d %s", rb.Code, rb.Body.String())
	}

	// Re-plan and test draining with an active job, then fencing and restart persistence.
	planRec = call(t, h, http.MethodPost, "/v1/handoff/plan", adminSecret, "", HandoffPlanRequest{
		TargetNodeID: linuxCred.NodeID, Model: "qwen.gguf", ModelBytes: 4 << 30,
		NewHostURL: "https://192.168.1.20:8443", NewHostCertSHA256: certDigest,
	})
	if planRec.Code != http.StatusOK {
		t.Fatalf("re-plan failed: %d", planRec.Code)
	}
	// Enqueue a job before starting handoff so handoff stays in draining until it finishes.
	jobRec := call(t, h, http.MethodPost, "/v1/jobs", adminSecret, "", protocol.ChatRequest{
		Model: "qwen.gguf", Messages: []protocol.Message{{Role: "user", Content: "hi"}},
	})
	if jobRec.Code != http.StatusAccepted {
		t.Fatalf("job submit failed: %d %s", jobRec.Code, jobRec.Body.String())
	}
	var job protocol.Job
	_ = json.Unmarshal(jobRec.Body.Bytes(), &job)

	startRec := call(t, h, http.MethodPost, "/v1/handoff/start", adminSecret, "", map[string]any{"confirm": true, "staged_verified": true})
	if startRec.Code != http.StatusOK {
		t.Fatalf("start handoff failed: %d %s", startRec.Code, startRec.Body.String())
	}
	if s.HandoffStatus().Phase != HandoffDraining {
		t.Fatalf("expected draining while job queued, got %s", s.HandoffStatus().Phase)
	}
	// New jobs must be refused while draining.
	duringDrain := call(t, h, http.MethodPost, "/v1/jobs", adminSecret, "", protocol.ChatRequest{
		Model: "qwen.gguf", Messages: []protocol.Message{{Role: "user", Content: "blocked"}},
	})
	if duringDrain.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 during drain, got %d", duringDrain.Code)
	}
	// Cancel active job and advance -> fenced!
	_ = call(t, h, http.MethodDelete, "/v1/jobs/"+job.ID, adminSecret, "", nil)
	st, err := s.AdvanceHandoff()
	if err != nil || st.Phase != HandoffFenced || st.Transition == nil {
		t.Fatalf("expected fenced with transition, got st=%+v err=%v", st, err)
	}

	// Verify EpochTransition against old coordinator authority pubkey.
	oldPubHex := s.AuthoritySnapshot().AuthorityPubKey
	oldPubBytes, _ := hex.DecodeString(oldPubHex)
	if err := VerifyEpochTransition(*st.Transition, ed25519.PublicKey(oldPubBytes), s.AuthoritySnapshot().ClusterID, 1, time.Now().UTC()); err != nil {
		t.Fatalf("valid EpochTransition rejected: %v", err)
	}
	tampered := *st.Transition
	tampered.NewHostURL = "https://10.0.0.99:8443"
	if err := VerifyEpochTransition(tampered, ed25519.PublicKey(oldPubBytes), s.AuthoritySnapshot().ClusterID, 1, time.Now().UTC()); err == nil {
		t.Fatal("tampered EpochTransition accepted")
	}

	// Cancel must be refused after fencing; rollback must be refused without new_authority_fenced=true.
	if cancelRec := call(t, h, http.MethodPost, "/v1/handoff/cancel", adminSecret, "", nil); cancelRec.Code != http.StatusConflict {
		t.Fatalf("expected cancel refused after fencing, got %d", cancelRec.Code)
	}
	if badRB := call(t, h, http.MethodPost, "/v1/handoff/rollback", adminSecret, "", map[string]any{"new_authority_fenced": false}); badRB.Code != http.StatusConflict {
		t.Fatalf("expected unconfirmed post-fence rollback refused, got %d", badRB.Code)
	}

	// Restart coordinator from disk: fenced state and new epoch must survive restart.
	restarted, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.SetStore(storePath); err != nil {
		t.Fatal(err)
	}
	if !restarted.fenced || restarted.AuthoritySnapshot().Epoch != st.NewEpoch {
		t.Fatalf("restarted coordinator lost fenced/epoch state: fenced=%v epoch=%d", restarted.fenced, restarted.AuthoritySnapshot().Epoch)
	}
	if afterRestartJob := call(t, restarted.Handler(), http.MethodPost, "/v1/jobs", adminSecret, "", protocol.ChatRequest{
		Model: "qwen.gguf", Messages: []protocol.Message{{Role: "user", Content: "after"}},
	}); afterRestartJob.Code != http.StatusServiceUnavailable {
		t.Fatalf("fenced restarted coordinator admitted new job: %d", afterRestartJob.Code)
	}

	// Verify incomplete readiness fails, complete readiness commits.
	if badVerify := call(t, restarted.Handler(), http.MethodPost, "/v1/handoff/verify", adminSecret, "", ReadinessEvidence{
		ModelLoaded: true, TopologyReady: false, APIAuthenticated: true, GenerationProbeOK: true, ExpectedMembers: 2, ObservedMembers: 2,
	}); badVerify.Code != http.StatusConflict {
		t.Fatalf("incomplete readiness accepted: %d", badVerify.Code)
	}
	okVerify := call(t, restarted.Handler(), http.MethodPost, "/v1/handoff/verify", adminSecret, "", ReadinessEvidence{
		ModelLoaded: true, TopologyReady: true, APIAuthenticated: true, GenerationProbeOK: true, ExpectedMembers: 2, ObservedMembers: 2,
	})
	if okVerify.Code != http.StatusOK || restarted.HandoffStatus().Phase != HandoffCommitted {
		t.Fatalf("expected committed after full readiness, got %d %s", okVerify.Code, okVerify.Body.String())
	}

	// Confirmed rollback advances epoch and restores original coordinator cleanly (B->A).
	rbOK := call(t, restarted.Handler(), http.MethodPost, "/v1/handoff/rollback", adminSecret, "", map[string]any{"new_authority_fenced": true})
	if rbOK.Code != http.StatusOK || restarted.fenced || restarted.AuthoritySnapshot().Epoch != st.NewEpoch+1 {
		t.Fatalf("confirmed rollback failed: %d fenced=%v epoch=%d", rbOK.Code, restarted.fenced, restarted.AuthoritySnapshot().Epoch)
	}
}

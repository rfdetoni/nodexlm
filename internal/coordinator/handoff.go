// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

type HandoffPhase string

const (
	HandoffIdle       HandoffPhase = "idle"
	HandoffPreflight  HandoffPhase = "preflight"
	HandoffStaging    HandoffPhase = "staging"
	HandoffDraining   HandoffPhase = "draining"
	HandoffFenced     HandoffPhase = "fenced"
	HandoffPromoting  HandoffPhase = "promoting"
	HandoffRejoining  HandoffPhase = "rejoining"
	HandoffVerifying  HandoffPhase = "verifying"
	HandoffCommitted  HandoffPhase = "committed"
	HandoffFailed     HandoffPhase = "failed"
	HandoffRolledBack HandoffPhase = "rolled_back"
)

type PortableConfig struct {
	Model          string `json:"model,omitempty"`
	ContextTokens  int    `json:"context_tokens,omitempty"`
	GPULayers      string `json:"gpu_layers,omitempty"`
	Threads        int    `json:"threads,omitempty"`
	ParallelSlots  int    `json:"parallel_slots,omitempty"`
	KVCacheType    string `json:"kv_cache_type,omitempty"`
	FlashAttention string `json:"flash_attention,omitempty"`
}

type ReadinessEvidence struct {
	ModelLoaded       bool `json:"model_loaded"`
	TopologyReady     bool `json:"topology_ready"`
	APIAuthenticated  bool `json:"api_authenticated"`
	GenerationProbeOK bool `json:"generation_probe_ok"`
	ExpectedMembers   int  `json:"expected_members"`
	ObservedMembers   int  `json:"observed_members"`
}

type EpochTransition struct {
	Version            int       `json:"version"`
	ClusterID          string    `json:"cluster_id"`
	OldEpoch           uint64    `json:"old_epoch"`
	NewEpoch           uint64    `json:"new_epoch"`
	TargetNodeID       string    `json:"target_node_id"`
	NewHostURL         string    `json:"new_host_url"`
	NewHostCertSHA256  string    `json:"new_host_cert_sha256"`
	NewAuthorityPubKey string    `json:"new_authority_pubkey"`
	IssuedAt           time.Time `json:"issued_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	Signature          string    `json:"signature,omitempty"`
}

func (t EpochTransition) canonicalBytes() ([]byte, error) {
	u := t
	u.Signature = ""
	u.IssuedAt = u.IssuedAt.UTC().Truncate(time.Millisecond)
	u.ExpiresAt = u.ExpiresAt.UTC().Truncate(time.Millisecond)
	return json.Marshal(u)
}

func VerifyEpochTransition(t EpochTransition, oldPub ed25519.PublicKey, expectedCluster string, currentEpoch uint64, now time.Time) error {
	if len(oldPub) != ed25519.PublicKeySize {
		return errors.New("missing pinned authority key")
	}
	if t.Version != 1 || t.ClusterID == "" || t.ClusterID != expectedCluster ||
		t.OldEpoch < currentEpoch || t.NewEpoch <= t.OldEpoch ||
		t.TargetNodeID == "" || !strings.HasPrefix(t.NewHostURL, "https://") ||
		!protocol.IsSHA256Hex(t.NewHostCertSHA256) {
		return errors.New("invalid epoch transition fields")
	}
	newPub, err := hex.DecodeString(t.NewAuthorityPubKey)
	if err != nil || len(newPub) != ed25519.PublicKeySize {
		return errors.New("invalid new authority public key")
	}
	if !t.ExpiresAt.After(t.IssuedAt) || now.Before(t.IssuedAt.Add(-30*time.Second)) || !now.Before(t.ExpiresAt) {
		return errors.New("epoch transition expired or invalid clock window")
	}
	sig, err := hex.DecodeString(t.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("invalid epoch transition signature")
	}
	payload, err := t.canonicalBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(oldPub, payload, sig) {
		return errors.New("epoch transition signature mismatch")
	}
	return nil
}

type HandoffPlanRequest struct {
	TargetNodeID       string         `json:"target_node_id"`
	Model              string         `json:"model"`
	ModelBytes         uint64         `json:"model_bytes,omitempty"`
	NewHostURL         string         `json:"new_host_url"`
	NewHostCertSHA256  string         `json:"new_host_cert_sha256"`
	NewAuthorityPubKey string         `json:"new_authority_pubkey,omitempty"`
	ForceAndroid       bool           `json:"force_android,omitempty"`
	AutomaticFailover  bool           `json:"automatic_failover,omitempty"`
	DrainTimeoutSec    int            `json:"drain_timeout_sec,omitempty"`
	Config             PortableConfig `json:"config,omitempty"`
}

type HandoffState struct {
	ID                 string             `json:"id,omitempty"`
	Phase              HandoffPhase       `json:"phase"`
	TargetNodeID       string             `json:"target_node_id,omitempty"`
	TargetName         string             `json:"target_name,omitempty"`
	TargetPlatform     string             `json:"target_platform,omitempty"`
	Model              string             `json:"model,omitempty"`
	ModelBytes         uint64             `json:"model_bytes,omitempty"`
	NewHostURL         string             `json:"new_host_url,omitempty"`
	NewHostCertSHA256  string             `json:"new_host_cert_sha256,omitempty"`
	NewAuthorityPubKey string             `json:"new_authority_pubkey,omitempty"`
	OldEpoch           uint64             `json:"old_epoch,omitempty"`
	NewEpoch           uint64             `json:"new_epoch,omitempty"`
	DrainDeadline      *time.Time         `json:"drain_deadline,omitempty"`
	Config             PortableConfig     `json:"config,omitempty"`
	Readiness          *ReadinessEvidence `json:"readiness,omitempty"`
	Transition         *EpochTransition   `json:"transition,omitempty"`
	ImpactSummary      string             `json:"impact_summary,omitempty"`
	Error              string             `json:"error,omitempty"`
	UpdatedAt          time.Time          `json:"updated_at,omitempty"`
}

func (s *Server) handoffPathLocked() string {
	if s.storePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.storePath), "handoff_state.json")
}

func (s *Server) loadHandoffLocked() error {
	path := s.handoffPathLocked()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var st HandoffState
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	s.handoff = st
	switch st.Phase {
	case HandoffDraining, HandoffStaging:
		s.draining = true
	case HandoffFenced, HandoffPromoting, HandoffRejoining, HandoffVerifying, HandoffCommitted:
		s.fenced = true
		s.draining = true
	}
	return nil
}

func (s *Server) saveHandoffLocked() error {
	path := s.handoffPathLocked()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(s.handoff)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".handoff-")
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

func (s *Server) activeJobsCountLocked() int {
	n := 0
	for _, j := range s.jobs {
		if j != nil && (j.Status == "running" || j.Status == "queued") {
			n++
		}
	}
	return n
}

func (s *Server) PlanHandoff(req HandoffPlanRequest) (HandoffState, error) {
	if req.TargetNodeID == "" || req.Model == "" || !strings.HasPrefix(req.NewHostURL, "https://") ||
		!protocol.IsSHA256Hex(req.NewHostCertSHA256) {
		return HandoffState{}, errors.New("target_node_id, model, https new_host_url, and new_host_cert_sha256 are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fenced {
		return HandoffState{}, errors.New("coordinator is already fenced at a newer epoch")
	}
	if s.handoff.Phase != "" && s.handoff.Phase != HandoffIdle && s.handoff.Phase != HandoffCommitted &&
		s.handoff.Phase != HandoffFailed && s.handoff.Phase != HandoffRolledBack {
		return HandoffState{}, fmt.Errorf("handoff already active in phase %s", s.handoff.Phase)
	}
	target := s.nodes[req.TargetNodeID]
	if target == nil {
		return HandoffState{}, errors.New("target node not enrolled")
	}
	if target.device.Status != "online" || time.Since(target.device.LastSeen) > heartbeatGrace {
		return HandoffState{}, errors.New("target node is offline")
	}
	if strings.EqualFold(target.device.Platform, "android") && !req.ForceAndroid {
		return HandoffState{}, errors.New("automatic promotion of Android device refused by stability policy (set force_android=true to override)")
	}
	if req.AutomaticFailover && len(s.nodes) < 2 {
		return HandoffState{}, errors.New("automatic failover refused in 2-node cluster without independent witness; use explicit confirmed handoff")
	}
	if !target.device.Runtime.Installed {
		return HandoffState{}, errors.New("target node runtime is not installed")
	}
	if req.ModelBytes > 0 {
		usable := target.device.AvailableRAMBytes
		if usable == 0 {
			usable = target.device.RAMBytes
		}
		if usable < req.ModelBytes+target.device.MemoryReserveBytes {
			return HandoffState{}, errors.New("target node lacks RAM + reserve margin for model and KV")
		}
	}
	newPubHex := req.NewAuthorityPubKey
	if newPubHex == "" {
		newPubHex = hex.EncodeToString(s.authorityPub)
	}
	if pubRaw, err := hex.DecodeString(newPubHex); err != nil || len(pubRaw) != ed25519.PublicKeySize {
		return HandoffState{}, errors.New("invalid new_authority_pubkey")
	}
	id, err := token()
	if err != nil {
		return HandoffState{}, err
	}
	timeoutSec := req.DrainTimeoutSec
	if timeoutSec <= 0 || timeoutSec > 300 {
		timeoutSec = 30
	}
	deadline := time.Now().UTC().Add(time.Duration(timeoutSec) * time.Second)
	st := HandoffState{
		ID:                 id[:16],
		Phase:              HandoffPreflight,
		TargetNodeID:       req.TargetNodeID,
		TargetName:         target.device.Name,
		TargetPlatform:     target.device.Platform,
		Model:              req.Model,
		ModelBytes:         req.ModelBytes,
		NewHostURL:         strings.TrimRight(req.NewHostURL, "/"),
		NewHostCertSHA256:  req.NewHostCertSHA256,
		NewAuthorityPubKey: newPubHex,
		OldEpoch:           s.epoch,
		NewEpoch:           s.epoch + 1,
		DrainDeadline:      &deadline,
		Config: PortableConfig{
			Model:          req.Model,
			ContextTokens:  req.Config.ContextTokens,
			GPULayers:      req.Config.GPULayers,
			Threads:        req.Config.Threads,
			ParallelSlots:  req.Config.ParallelSlots,
			KVCacheType:    req.Config.KVCacheType,
			FlashAttention: req.Config.FlashAttention,
		},
		ImpactSummary: fmt.Sprintf("Promote %s (%s) to coordinator epoch %d; active_jobs=%d; existing Guests must rejoin.",
			target.device.Name, req.TargetNodeID, s.epoch+1, s.activeJobsCountLocked()),
		UpdatedAt: time.Now().UTC(),
	}
	prev := s.handoff
	s.handoff = st
	if err := s.saveHandoffLocked(); err != nil {
		s.handoff = prev
		return HandoffState{}, err
	}
	return st, nil
}

func (s *Server) StartHandoff(confirm bool, stagedVerified bool) (HandoffState, error) {
	if !confirm {
		return HandoffState{}, errors.New("explicit confirmation required to switch coordinator role")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handoff.Phase != HandoffPreflight {
		return HandoffState{}, fmt.Errorf("handoff must be in preflight phase, got %s", s.handoff.Phase)
	}
	if !stagedVerified {
		s.handoff.Phase = HandoffFailed
		s.handoff.Error = "target model staging SHA-256 verification not confirmed"
		s.handoff.UpdatedAt = time.Now().UTC()
		_ = s.saveHandoffLocked()
		return s.handoff, errors.New(s.handoff.Error)
	}
	prev := s.handoff
	s.handoff.Phase = HandoffStaging
	s.handoff.UpdatedAt = time.Now().UTC()
	s.draining = true
	if err := s.saveHandoffLocked(); err != nil {
		s.handoff = prev
		s.draining = false
		return HandoffState{}, err
	}

	s.handoff.Phase = HandoffDraining
	s.handoff.UpdatedAt = time.Now().UTC()
	if err := s.saveHandoffLocked(); err != nil {
		s.handoff = prev
		s.draining = false
		return HandoffState{}, err
	}

	if active := s.activeJobsCountLocked(); active > 0 {
		if s.handoff.DrainDeadline != nil && time.Now().UTC().After(*s.handoff.DrainDeadline) {
			s.handoff.Phase = HandoffFailed
			s.handoff.Error = fmt.Sprintf("drain deadline exceeded with %d active jobs", active)
			s.handoff.UpdatedAt = time.Now().UTC()
			s.draining = false
			_ = s.saveHandoffLocked()
			return s.handoff, errors.New(s.handoff.Error)
		}
		// Stay in draining until active jobs finish or caller advances/cancels.
		return s.handoff, nil
	}
	return s.fenceAndPromoteLocked()
}

func (s *Server) AdvanceHandoff() (HandoffState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.handoff.Phase {
	case HandoffDraining:
		if active := s.activeJobsCountLocked(); active > 0 {
			if s.handoff.DrainDeadline != nil && time.Now().UTC().After(*s.handoff.DrainDeadline) {
				s.handoff.Phase = HandoffFailed
				s.handoff.Error = fmt.Sprintf("drain deadline exceeded with %d active jobs", active)
				s.handoff.UpdatedAt = time.Now().UTC()
				s.draining = false
				_ = s.saveHandoffLocked()
				return s.handoff, errors.New(s.handoff.Error)
			}
			return s.handoff, nil
		}
		return s.fenceAndPromoteLocked()
	case HandoffFenced:
		s.handoff.Phase = HandoffPromoting
		s.handoff.UpdatedAt = time.Now().UTC()
		_ = s.saveHandoffLocked()
		return s.handoff, nil
	case HandoffPromoting:
		s.handoff.Phase = HandoffRejoining
		s.handoff.UpdatedAt = time.Now().UTC()
		_ = s.saveHandoffLocked()
		return s.handoff, nil
	case HandoffRejoining:
		s.handoff.Phase = HandoffVerifying
		s.handoff.UpdatedAt = time.Now().UTC()
		_ = s.saveHandoffLocked()
		return s.handoff, nil
	default:
		return s.handoff, fmt.Errorf("cannot advance handoff from phase %s", s.handoff.Phase)
	}
}

func (s *Server) fenceAndPromoteLocked() (HandoffState, error) {
	now := time.Now().UTC()
	tr := EpochTransition{
		Version:            1,
		ClusterID:          s.clusterID,
		OldEpoch:           s.epoch,
		NewEpoch:           s.handoff.NewEpoch,
		TargetNodeID:       s.handoff.TargetNodeID,
		NewHostURL:         s.handoff.NewHostURL,
		NewHostCertSHA256:  s.handoff.NewHostCertSHA256,
		NewAuthorityPubKey: s.handoff.NewAuthorityPubKey,
		IssuedAt:           now,
		ExpiresAt:          now.Add(15 * time.Minute),
	}
	payload, err := tr.canonicalBytes()
	if err != nil {
		return HandoffState{}, err
	}
	tr.Signature = hex.EncodeToString(ed25519.Sign(s.authorityPriv, payload))

	prevEpoch := s.epoch
	prevHandoff := s.handoff
	s.epoch = s.handoff.NewEpoch
	s.fenced = true
	s.draining = true
	s.handoff.Phase = HandoffFenced
	s.handoff.Transition = &tr
	s.handoff.UpdatedAt = now
	if err := s.saveAuthorityLocked(); err != nil {
		s.epoch = prevEpoch
		s.fenced = false
		s.handoff = prevHandoff
		return HandoffState{}, err
	}
	if err := s.saveHandoffLocked(); err != nil {
		s.epoch = prevEpoch
		s.fenced = false
		s.handoff = prevHandoff
		_ = s.saveAuthorityLocked()
		return HandoffState{}, err
	}
	return s.handoff, nil
}

func (s *Server) VerifyHandoffReadiness(ev ReadinessEvidence) (HandoffState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handoff.Phase != HandoffFenced && s.handoff.Phase != HandoffPromoting &&
		s.handoff.Phase != HandoffRejoining && s.handoff.Phase != HandoffVerifying {
		return HandoffState{}, fmt.Errorf("readiness check requires fenced/promoting/rejoining/verifying phase, got %s", s.handoff.Phase)
	}
	if !ev.ModelLoaded || !ev.TopologyReady || !ev.APIAuthenticated || !ev.GenerationProbeOK ||
		ev.ExpectedMembers <= 0 || ev.ObservedMembers < ev.ExpectedMembers {
		return HandoffState{}, errors.New("readiness verification incomplete: model, topology, auth API, generation probe, and expected membership all required")
	}
	s.handoff.Readiness = &ev
	s.handoff.Phase = HandoffCommitted
	s.handoff.UpdatedAt = time.Now().UTC()
	if err := s.saveHandoffLocked(); err != nil {
		return HandoffState{}, err
	}
	return s.handoff, nil
}

func (s *Server) CancelHandoff() (HandoffState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handoff.Phase == HandoffFenced || s.handoff.Phase == HandoffPromoting ||
		s.handoff.Phase == HandoffRejoining || s.handoff.Phase == HandoffVerifying || s.handoff.Phase == HandoffCommitted {
		return HandoffState{}, errors.New("cannot cancel after fencing; use rollback after confirming new Host fencing")
	}
	s.draining = false
	s.handoff.Phase = HandoffRolledBack
	s.handoff.Error = "cancelled before fencing"
	s.handoff.UpdatedAt = time.Now().UTC()
	if err := s.saveHandoffLocked(); err != nil {
		return HandoffState{}, err
	}
	return s.handoff, nil
}

func (s *Server) RollbackHandoff(newAuthorityFenced bool) (HandoffState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.handoff.Phase {
	case HandoffPreflight, HandoffStaging, HandoffDraining, HandoffFailed:
		s.draining = false
		s.handoff.Phase = HandoffRolledBack
		s.handoff.UpdatedAt = time.Now().UTC()
		if err := s.saveHandoffLocked(); err != nil {
			return HandoffState{}, err
		}
		return s.handoff, nil
	case HandoffFenced, HandoffPromoting, HandoffRejoining, HandoffVerifying, HandoffCommitted:
		if !newAuthorityFenced {
			return HandoffState{}, errors.New("rollback after fencing refused without confirmed fencing of target authority")
		}
		s.epoch++
		s.fenced = false
		s.draining = false
		s.handoff.Phase = HandoffRolledBack
		s.handoff.Error = fmt.Sprintf("rolled back to original coordinator at epoch %d; re-enrolled nodes require fresh epoch trust", s.epoch)
		s.handoff.UpdatedAt = time.Now().UTC()
		if err := s.saveAuthorityLocked(); err != nil {
			return HandoffState{}, err
		}
		if err := s.saveHandoffLocked(); err != nil {
			return HandoffState{}, err
		}
		return s.handoff, nil
	default:
		return HandoffState{}, fmt.Errorf("nothing to rollback in phase %s", s.handoff.Phase)
	}
}

func (s *Server) HandoffStatus() HandoffState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handoff.Phase == "" {
		return HandoffState{Phase: HandoffIdle}
	}
	return s.handoff
}

func (s *Server) handoffPlanHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	var req HandoffPlanRequest
	if !parse(w, r, &req) {
		return
	}
	st, err := s.PlanHandoff(req)
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, http.StatusOK, st)
}

func (s *Server) handoffStartHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	var req struct {
		Confirm        bool `json:"confirm"`
		StagedVerified bool `json:"staged_verified"`
	}
	if !parse(w, r, &req) {
		return
	}
	st, err := s.StartHandoff(req.Confirm, req.StagedVerified)
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, http.StatusOK, st)
}

func (s *Server) handoffStatusHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	respond(w, http.StatusOK, s.HandoffStatus())
}

func (s *Server) handoffCancelHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	st, err := s.CancelHandoff()
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, http.StatusOK, st)
}

func (s *Server) handoffRollbackHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	var req struct {
		NewAuthorityFenced bool `json:"new_authority_fenced"`
	}
	if !parse(w, r, &req) {
		return
	}
	st, err := s.RollbackHandoff(req.NewAuthorityFenced)
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, http.StatusOK, st)
}

func (s *Server) handoffVerifyHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	var ev ReadinessEvidence
	if !parse(w, r, &ev) {
		return
	}
	st, err := s.VerifyHandoffReadiness(ev)
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, http.StatusOK, st)
}

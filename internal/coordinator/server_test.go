// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

const (
	adminSecret = "adm-0123456789abcdefghijklmnopqrstuvwxyz"
	joinSecret  = "join-0123456789abcdefghijklmnopqrstuvwxyz"
)

func call(t *testing.T, h http.Handler, method, path, token, node string, value any) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if value == nil {
		body = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if node != "" {
		req.Header.Set("X-Nodex-Node", node)
	}
	req.RemoteAddr = "127.0.0.1:22334"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
func TestEnrollLeaseCompletionAndRestart(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := s.SetStore(path); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	unauthorized := call(t, h, "GET", "/v1/nodes", "bad", "", nil)
	if unauthorized.Code != 401 {
		t.Fatalf("admin guard: %d", unauthorized.Code)
	}
	join := call(t, h, "POST", "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{Device: protocol.Device{Name: "device", Platform: "android", CPUCount: 6, Models: []string{"tiny.gguf"}}})
	if join.Code != 201 {
		t.Fatalf("join: %d %s", join.Code, join.Body.String())
	}
	var creds protocol.Credential
	if err := json.Unmarshal(join.Body.Bytes(), &creds); err != nil {
		t.Fatal(err)
	}
	if creds.Token == joinSecret || creds.Token == "" {
		t.Fatal("per-node credential not issued")
	}
	invalid := call(t, h, "POST", "/v1/nodes/lease", "wrong", creds.NodeID, map[string][]string{"models": {"tiny.gguf"}})
	if invalid.Code != 401 {
		t.Fatal("node lease accepted invalid credential")
	}
	input := protocol.ChatRequest{Model: "tiny.gguf", Messages: []protocol.Message{{Role: "user", Content: "hello"}}}
	enq := call(t, h, "POST", "/v1/jobs", adminSecret, "", input)
	if enq.Code != 202 {
		t.Fatalf("queue: %d %s", enq.Code, enq.Body.String())
	}
	var job protocol.Job
	if err := json.Unmarshal(enq.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	lease := call(t, h, "POST", "/v1/nodes/lease", creds.Token, creds.NodeID, map[string][]string{"models": {"tiny.gguf"}})
	if lease.Code != 200 {
		t.Fatalf("lease: %d %s", lease.Code, lease.Body.String())
	}
	var assigned protocol.Job
	if err := json.Unmarshal(lease.Body.Bytes(), &assigned); err != nil {
		t.Fatal(err)
	}
	if assigned.ID != job.ID || assigned.NodeID != creds.NodeID {
		t.Fatal("wrong assigned job")
	}
	badResult := call(t, h, "POST", "/v1/nodes/result/"+job.ID, "wrong", creds.NodeID, protocol.JobResult{Result: "test"})
	if badResult.Code != 401 {
		t.Fatal("unauthorized result accepted")
	}
	success := call(t, h, "POST", "/v1/nodes/result/"+job.ID, creds.Token, creds.NodeID, protocol.JobResult{Result: "hello from model"})
	if success.Code != 200 {
		t.Fatalf("result: %d %s", success.Code, success.Body.String())
	}
	got := call(t, h, "GET", "/v1/jobs/"+job.ID, adminSecret, "", nil)
	if got.Code != 200 {
		t.Fatal("job query failed")
	}
	var finished protocol.Job
	_ = json.Unmarshal(got.Body.Bytes(), &finished)
	if finished.Status != "completed" || finished.Result != "hello from model" {
		t.Fatalf("wrong completion: %+v", finished)
	}
	s2, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.SetStore(path); err != nil {
		t.Fatal(err)
	}
	listed := call(t, s2.Handler(), "GET", "/v1/nodes", adminSecret, "", nil)
	var devices []protocol.Device
	_ = json.Unmarshal(listed.Body.Bytes(), &devices)
	if len(devices) != 1 || devices[0].ID != creds.NodeID {
		t.Fatal("restart lost node identity")
	}
}
func TestRejections(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	bad := protocol.ChatRequest{Model: "m", Messages: []protocol.Message{{Role: "user", Content: "x"}}, Stream: true}
	res := call(t, h, "POST", "/v1/jobs", adminSecret, "", bad)
	if res.Code != 400 {
		t.Fatalf("unimplemented stream accepted: %d", res.Code)
	}
	if _, err := New("short", "short"); err == nil {
		t.Fatal("weak secrets allowed")
	}
	s.mu.Lock()
	s.jobs["expired"] = &protocol.Job{ID: "expired", Status: "queued", Created: time.Now().Add(-10 * time.Minute)}
	s.queue = append(s.queue, "expired")
	s.mu.Unlock()
	// Cleanup is exercised with normal timed state by the integration tests.
}

func TestCatalogRequiresPairedNode(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	response := call(t, s.Handler(), "GET", "/v1/nodes/catalog?q=Qwen", "", "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

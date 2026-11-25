package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func decisionTestNode(t *testing.T, server *Server, name string) protocol.Credential {
	t.Helper()
	reply := call(t, server.Handler(), "POST", "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{
		Device: protocol.Device{Name: name, Platform: "linux", CPUCount: 4,
			DecisionModels: []string{"ollaya/laya:multilingual"}, Models: []string{"chat.gguf"},
			RAMBytes: 8 << 30, AvailableRAMBytes: 4 << 30},
	})
	if reply.Code != 201 {
		t.Fatalf("enroll %d %s", reply.Code, reply.Body.String())
	}
	var cred protocol.Credential
	if err := json.Unmarshal(reply.Body.Bytes(), &cred); err != nil {
		t.Fatal(err)
	}
	return cred
}

func TestDecisionWorkersRequireFreshAuthenticatedCapability(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseRPC()
	if _, err := s.Decide(context.Background(), "ollaya/laya:multilingual", []byte("{}")); !errors.Is(err, ErrNoDecisionWorkers) {
		t.Fatalf("expected unavailable worker: %v", err)
	}
	cred := decisionTestNode(t, s, "phone")
	if workers := s.DecisionWorkers(); len(workers) != 1 || workers[0].Model != "ollaya/laya:multilingual" {
		t.Fatalf("missing capability: %+v", workers)
	}
	s.mu.Lock()
	s.nodes[cred.NodeID].decisionSeen = time.Now().Add(-heartbeatGrace - time.Second)
	s.mu.Unlock()
	if workers := s.DecisionWorkers(); len(workers) != 0 {
		t.Fatal("stale Android decision worker still announced")
	}
	if _, err := s.Decide(context.Background(), "ollaya/laya:multilingual", []byte("{}")); !errors.Is(err, ErrNoDecisionWorkers) {
		t.Fatalf("stale worker selected: %v", err)
	}
}

func TestTypedDecisionsFailOverAndIgnoreLateStaleResults(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseRPC()
	a := decisionTestNode(t, s, "node-a")
	b := decisionTestNode(t, s, "node-b")
	nodes := map[string]protocol.Credential{a.NodeID: a, b.NodeID: b}
	candidates := s.decisionCandidates("ollaya/laya:multilingual")
	if len(candidates) != 2 {
		t.Fatalf("wrong candidates: %+v", candidates)
	}
	first, second := nodes[candidates[0].id], nodes[candidates[1].id]
	body := []byte(`{"model":"ollaya/laya:multilingual","state":"Hi","questions":{"q":{"type":"noul","instructions":"Hi?"}}}`)
	type outcome struct {
		raw []byte
		err error
	}
	done := make(chan outcome, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() { raw, err := s.Decide(ctx, "ollaya/laya:multilingual", body); done <- outcome{raw, err} }()
	firstLease := leaseDecisionForTest(t, s, first)
	if firstLease.Kind != "decision" || firstLease.Request.Model != "ollaya/laya:multilingual" ||
		string(firstLease.Decision) != string(body) {
		t.Fatalf("typed job lost: %+v", firstLease)
	}
	// Deliberate failure on first worker triggers retry on the next best node.
	r := call(t, s.Handler(), "POST", "/v1/nodes/result/"+firstLease.ID, first.Token, first.NodeID,
		protocol.JobResult{Error: "runtime unhealthy"})
	if r.Code != 200 {
		t.Fatalf("failure ack %d %s", r.Code, r.Body)
	}
	secondLease := leaseDecisionForTest(t, s, second)
	if secondLease.NodeID != second.NodeID {
		t.Fatal("fallback did not use the second node")
	}
	invalid := call(t, s.Handler(), "POST", "/v1/nodes/result/"+secondLease.ID, first.Token, first.NodeID,
		protocol.JobResult{Result: `{"answers":{}}`})
	if invalid.Code != 409 {
		t.Fatalf("unrelated worker submitted result: %d", invalid.Code)
	}
	success := call(t, s.Handler(), "POST", "/v1/nodes/result/"+secondLease.ID, second.Token, second.NodeID,
		protocol.JobResult{Result: `{"answers":{"q":{"noul":0.93}}}`})
	if success.Code != 200 {
		t.Fatalf("decision result %d %s", success.Code, success.Body)
	}
	select {
	case outcome := <-done:
		if outcome.err != nil || !strings.Contains(string(outcome.raw), "0.93") {
			t.Fatalf("bad failover: %s %v", outcome.raw, outcome.err)
		}
	case <-ctx.Done():
		t.Fatal("decision failed to complete")
	}
	workers := s.DecisionWorkers()
	found := false
	for _, n := range workers {
		if n.ID == second.NodeID && n.P50MS >= 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("decision latency missing from worker inventory")
	}
}

func leaseDecisionForTest(t *testing.T, s *Server, c protocol.Credential) protocol.Job {
	t.Helper()
	reply := call(t, s.Handler(), http.MethodPost, "/v1/nodes/lease", c.Token, c.NodeID,
		protocol.Device{DecisionModels: []string{"ollaya/laya:multilingual"}})
	if reply.Code != 200 {
		t.Fatalf("decision lease %d %s", reply.Code, reply.Body.String())
	}
	var job protocol.Job
	if err := json.Unmarshal(reply.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return job
}

func TestDecisionCapabilityRejectsWildcardAndMalformedPayloads(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseRPC()
	for _, model := range []string{"*", "bad\nheader", strings.Repeat("x", 257)} {
		join := call(t, s.Handler(), "POST", "/v1/nodes/enroll", joinSecret, "",
			protocol.Enrollment{Device: protocol.Device{Name: "n", Platform: "linux", CPUCount: 2, DecisionModels: []string{model}}})
		if join.Code != 400 {
			t.Fatalf("accepted unsafe capability %q: %d", model, join.Code)
		}
	}
}

func TestAdaptiveDecisionRoutingAvoidsHotAndBusyWorkers(t *testing.T) {
	t.Setenv("NODEXLM_DECISION_ADAPTIVE", "1")
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseRPC()
	hot := decisionTestNode(t, s, "hot")
	cold := decisionTestNode(t, s, "cold")
	temperature := 4
	s.mu.Lock()
	s.nodes[hot.NodeID].device.ThermalStatus = &temperature
	s.decisionTimings[hot.NodeID] = []time.Duration{100 * time.Millisecond}
	s.decisionTimings[cold.NodeID] = []time.Duration{200 * time.Millisecond}
	s.nodes[hot.NodeID].decisionEWMA = 100 * time.Millisecond
	s.nodes[cold.NodeID].decisionEWMA = 200 * time.Millisecond
	s.mu.Unlock()
	choices := s.decisionCandidates("ollaya/laya:multilingual")
	if len(choices) != 2 || choices[0].id != cold.NodeID {
		t.Fatalf("hot worker outranked cold equivalent: %+v", choices)
	}
	s.mu.Lock()
	s.nodes[cold.NodeID].active = "busy"
	s.mu.Unlock()
	choices = s.decisionCandidates("ollaya/laya:multilingual")
	if choices[0].id != hot.NodeID || choices[0].inFlight != 0 {
		t.Fatalf("busy worker outranked idle: %+v", choices)
	}
}

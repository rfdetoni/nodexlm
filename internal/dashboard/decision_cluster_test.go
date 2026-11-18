package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/coordinator"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestDashboardRoutesTypedDecisionToAuthenticatedRemoteWorker(t *testing.T) {
	const admin = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const enrollment = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := t.TempDir()
	manager, err := NewManager(dir, filepath.Join(dir, "models"), "llama-server")
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := coordinator.New(admin, enrollment)
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.CloseRPC()
	host := New(manager, cluster, admin).Handler()
	enroll := req(host, http.MethodPost, "/v1/nodes/enroll", enrollment, "",
		protocol.Enrollment{Device: protocol.Device{
			Name: "linux-decision", Platform: "linux", CPUCount: 4,
			DecisionModels: []string{"ollaya/laya:en"},
		}})
	if enroll.Code != http.StatusCreated {
		t.Fatalf("enrollment failed %d %s", enroll.Code, enroll.Body.String())
	}
	var cred protocol.Credential
	if err := json.Unmarshal(enroll.Body.Bytes(), &cred); err != nil {
		t.Fatal(err)
	}
	if got := req(host, "GET", "/api/decision-workers", "bad", "", nil).Code; got != 401 {
		t.Fatalf("unprotected inventory %d", got)
	}
	if got := req(host, "GET", "/api/decision-workers", admin, "", nil).Code; got != 200 {
		t.Fatalf("admin inventory failed %d", got)
	}

	type outcome struct { status int; body, executor string }
	done := make(chan outcome, 1)
	go func() {
		rec := req(host, "POST", "/v1/systemone", admin, "", map[string]any{
			"model": "ollaya/laya:en", "state": "Refund requested",
			"questions": map[string]any{"urgent": map[string]any{"type":"noul","instructions":"urgent?"}},
		})
		done <- outcome{rec.Code, rec.Body.String(), rec.Header().Get("X-Nodexlm-Decision-Executor")}
	}()

	leaseBody, _ := json.Marshal(protocol.Device{DecisionModels: []string{"ollaya/laya:en"}})
	leaseReq := httptest.NewRequest("POST", "/v1/nodes/lease", strings.NewReader(string(leaseBody)))
	leaseReq.Header.Set("Content-Type", "application/json")
	leaseReq.Header.Set("Authorization", "Bearer "+cred.Token)
	leaseReq.Header.Set("X-Nodex-Node", cred.NodeID)
	lease := httptest.NewRecorder()
	cluster.Handler().ServeHTTP(lease, leaseReq)
	if lease.Code != 200 {
		t.Fatalf("decision lease failed: %d %s", lease.Code, lease.Body.String())
	}
	var job protocol.Job
	if err := json.Unmarshal(lease.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Kind != "decision" || !strings.Contains(string(job.Decision), "questions") {
		t.Fatalf("job not routed to worker: %+v", job)
	}
	resultBody, _ := json.Marshal(protocol.JobResult{Result: `{"answers":{"urgent":{"noul":0.87}}}`})
	post := httptest.NewRequest("POST", "/v1/nodes/result/"+job.ID, strings.NewReader(string(resultBody)))
	post.Header.Set("Content-Type", "application/json")
	post.Header.Set("Authorization", "Bearer "+cred.Token)
	post.Header.Set("X-Nodex-Node", cred.NodeID)
	ack := httptest.NewRecorder()
	cluster.Handler().ServeHTTP(ack, post)
	if ack.Code != 200 {
		t.Fatalf("worker completion failed %d %s", ack.Code, ack.Body.String())
	}
	select {
	case result := <-done:
		if result.status != 200 || !strings.Contains(result.body, "0.87") || result.executor != "cluster" {
			t.Fatalf("wrong cluster decision result: %+v", result)
		}
	case <-time.After(5*time.Second):
		t.Fatal("decision request did not complete")
	}
}

package coordinator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestDisconnectedChatReclaimsQueue(t *testing.T) {
	s, _ := New(adminSecret, joinSecret)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"test"}]}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+adminSecret)
	done := make(chan struct{})
	go func() { s.Handler().ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		count := len(s.queue)
		s.mu.Unlock()
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not queued")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) != 0 {
		t.Fatal("disconnected job leaked queue capacity")
	}
	for _, j := range s.jobs {
		if j.Status != "cancelled" {
			t.Fatal(j.Status)
		}
	}
}
func TestRevokedWorkerCannotFinishJob(t *testing.T) {
	s, _ := New(adminSecret, joinSecret)
	out := call(t, s.Handler(), http.MethodPost, "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{Device: protocol.Device{Name: "test", Platform: "linux", CPUCount: 1, Models: []string{"m"}}})
	var cred protocol.Credential
	_ = json.Unmarshal(out.Body.Bytes(), &cred)
	job, _ := s.queueJob(protocol.ChatRequest{Model: "m"})
	res := call(t, s.Handler(), http.MethodPost, "/v1/nodes/lease", cred.Token, cred.NodeID, protocol.Device{Models: []string{"m"}})
	if res.Code != 200 {
		t.Fatal(res.Code)
	}
	call(t, s.Handler(), http.MethodDelete, "/v1/nodes/"+cred.NodeID, adminSecret, "", nil)
	res = call(t, s.Handler(), http.MethodPost, "/v1/nodes/result/"+job.ID, cred.Token, cred.NodeID, protocol.JobResult{Result: "late"})
	if res.Code != 401 {
		t.Fatal(res.Code)
	}
}

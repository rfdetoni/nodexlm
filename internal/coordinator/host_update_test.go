package coordinator

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestHostRequiresExactGuestBuild(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseRPC()
	required := strings.Repeat("a", 40)
	s.RequireBuild(required, "0.1.0-edge.108")
	h := s.Handler()
	device := protocol.Device{Name: "android", Platform: "android", CPUCount: 4, BuildCommit: strings.Repeat("b", 40)}
	joined := call(t, h, http.MethodPost, "/v1/nodes/enroll", joinSecret, "", protocol.Enrollment{Device: device})
	if joined.Code != http.StatusCreated {
		t.Fatalf("enroll: %s", joined.Body.String())
	}
	var cred protocol.Credential
	if err := json.Unmarshal(joined.Body.Bytes(), &cred); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/v1/nodes/heartbeat", "/v1/nodes/lease"} {
		reply := call(t, h, http.MethodPost, route, cred.Token, cred.NodeID, device)
		var directive protocol.UpdateDirective
		if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &directive) != nil || !directive.UpdateRequired || directive.RequiredBuildCommit != required {
			t.Fatalf("missing required update: %s", reply.Body.String())
		}
	}
	if devices := s.DevicesSnapshot(); len(devices) != 1 || devices[0].Status != "updating" || devices[0].Runtime.RPCReady {
		t.Fatalf("incompatible node admitted: %+v", devices)
	}
	device.BuildCommit = required
	reply := call(t, h, http.MethodPost, "/v1/nodes/heartbeat", cred.Token, cred.NodeID, device)
	var directive protocol.UpdateDirective
	if reply.Code != 200 || json.Unmarshal(reply.Body.Bytes(), &directive) != nil || directive.UpdateRequired {
		t.Fatalf("updated Guest rejected: %s", reply.Body.String())
	}
	if directive.Runtime == nil {
		t.Fatal("missing signed runtime policy")
	}
	if err := protocol.VerifyRuntimePolicy(*directive.Runtime, cred, time.Now()); err != nil {
		t.Fatal(err)
	}
	if directive.Runtime.Policy.HostBuildCommit != required {
		t.Fatal("runtime policy not bound to Host build")
	}
	if devices := s.DevicesSnapshot(); devices[0].Status != "online" {
		t.Fatalf("updated Guest unavailable: %+v", devices)
	}
}

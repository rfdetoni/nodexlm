package coordinator

import (
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestTrellisPolicyIsDesktopOnly(t *testing.T) {
	s, err := New(adminSecret, joinSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseRPC()
	s.RequireBuild(strings.Repeat("a", 40), "test")
	s.nodes["desktop"] = &nodeState{device: protocol.Device{
		Platform: "linux", BuildCommit: s.buildCommit, Status: "online", LastSeen: time.Now(),
		Runtime: protocol.RuntimeCapability{RPCSourceCommit: llamaruntime.SourceCommit},
	}}
	s.nodes["mobile"] = &nodeState{device: protocol.Device{Platform: "android"}}
	t.Setenv("NODEXLM_EXPERIMENTAL_TRELLIS_GPU", "1")
	if got := s.SetTrellisRPC(true); got != 1 {
		t.Fatalf("eligible desktop count: %d", got)
	}
	if p := s.runtimePolicyLocked("desktop"); p == nil || p.Policy.Engine != "trellis" || p.Policy.RPCPatch != 3 || !p.Policy.ExperimentalGPU {
		t.Fatalf("unexpected desktop policy: %+v", p)
	}
	if p := s.runtimePolicyLocked("mobile"); p == nil || p.Policy.Engine != "llama.cpp" || p.Policy.ExperimentalGPU {
		t.Fatalf("unexpected Android policy: %+v", p)
	}
	s.SetTrellisRPC(false)
	if p := s.runtimePolicyLocked("desktop"); p == nil || p.Policy.Engine != "llama.cpp" || p.Policy.ExperimentalGPU {
		t.Fatalf("stock policy was not restored: %+v", p)
	}
}

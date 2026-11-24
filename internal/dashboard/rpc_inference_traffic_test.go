package dashboard

import (
	"testing"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

func TestInferenceTrafficRejectsResetOrMissingRelay(t *testing.T) {
	a := map[string]rpcmesh.TunnelStats{"node": {Epoch: 1, BytesToGuest: 100, BytesFromGuest: 20, WritesToGuest: 2}}
	b := map[string]rpcmesh.TunnelStats{"node": {Epoch: 1, BytesToGuest: 300, BytesFromGuest: 60, WritesToGuest: 5}}
	if got := inferenceTraffic([]string{"node"}, a, b); got == nil || got.BytesToGuest != 200 || got.BytesFromGuest != 40 || got.WritesToGuest != 3 {
		t.Fatalf("unexpected traffic: %+v", got)
	}
	if inferenceTraffic([]string{"missing"}, a, b) != nil {
		t.Fatal("missing relay accepted")
	}
	b["node"] = rpcmesh.TunnelStats{Epoch: 2, BytesToGuest: 400}
	if inferenceTraffic([]string{"node"}, a, b) != nil {
		t.Fatal("reset relay accepted")
	}
}

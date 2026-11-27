package dashboard

import (
	"testing"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

func TestInferenceTrafficRejectsResetOrMissingRelay(t *testing.T) {
	a := map[string]rpcmesh.TunnelStats{"node": {Epoch: 1, BytesToGuest: 100, BytesFromGuest: 20, WritesToGuest: 2}}
	b := map[string]rpcmesh.TunnelStats{"node": {Epoch: 1, BytesToGuest: 300, BytesFromGuest: 60, WritesToGuest: 5}}
	if got := inferenceTraffic([]string{"node"}, a, b); got == nil || got.BytesToGuest != 200 || got.BytesFromGuest != 40 || got.WritesToGuest != 3 ||
		len(got.Nodes) != 1 || got.Nodes[0].BytesToGuest != 200 || got.Nodes[0].BytesFromGuest != 40 ||
		got.Nodes[0].WritesToGuest != 3 || got.Nodes[0].Bytes != 240 {
		t.Fatalf("unexpected per-Guest traffic: %+v", got)
	}
	if inferenceTraffic([]string{"missing"}, a, b) != nil {
		t.Fatal("missing relay accepted")
	}
	b["node"] = rpcmesh.TunnelStats{Epoch: 2, BytesToGuest: 400}
	if inferenceTraffic([]string{"node"}, a, b) != nil {
		t.Fatal("reset relay accepted")
	}
}

func TestObservedRPCPhaseWindows(t *testing.T) {
	nodes := []string{"guest"}
	a := map[string]rpcmesh.TunnelStats{"guest": {Epoch: 1, BytesToGuest: 100, BytesFromGuest: 50}}
	b := map[string]rpcmesh.TunnelStats{"guest": {Epoch: 1, BytesToGuest: 200, BytesFromGuest: 80}}
	c := map[string]rpcmesh.TunnelStats{"guest": {Epoch: 1, BytesToGuest: 300, BytesFromGuest: 180}}
	prefill, decode := trafficWindow(nodes, a, b), trafficWindow(nodes, b, c)
	if prefill == nil || decode == nil || prefill.BytesToGuest != 100 || prefill.BytesFromGuest != 30 || decode.BytesToGuest != 100 || decode.BytesFromGuest != 100 {
		t.Fatalf("unexpected windows: %+v %+v", prefill, decode)
	}
	c["guest"] = rpcmesh.TunnelStats{Epoch: 2}
	if trafficWindow(nodes, b, c) != nil {
		t.Fatal("reset counters must not produce a phase measurement")
	}
}

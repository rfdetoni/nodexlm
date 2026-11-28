package dashboard

import (
	"reflect"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

func TestLostRPCGuestsOnlyConfirmedLoss(t *testing.T) {
	before := map[string]rpcmesh.TunnelStats{
		"healthy": {Epoch: 1, Connected: 2, Idle: 1},
		"gone":    {Epoch: 2, Connected: 1},
		"changed": {Epoch: 3, Connected: 1},
		"idle":    {Epoch: 4, Connected: 1, Idle: 1},
		"session": {Epoch: 5, Connected: 2, Idle: 1},
	}
	after := map[string]rpcmesh.TunnelStats{
		"healthy": {Epoch: 1, Connected: 1},
		"changed": {Epoch: 6, Connected: 1},
		"idle":    {Epoch: 4, Connected: 1, Idle: 1},
		"session": {Epoch: 5, Connected: 2, Idle: 2},
	}
	nodes := []string{"healthy", "gone", "changed", "idle", "session", "unknown"}
	if got := lostRPCGuests(nodes, before, after, true); !reflect.DeepEqual(got, []string{"gone", "changed", "session"}) {
		t.Fatalf("unexpected failed Guests: %v", got)
	}
	if got := lostRPCGuests(nodes, before, after, false); !reflect.DeepEqual(got, []string{"gone", "changed"}) {
		t.Fatalf("unexpected load-time failed Guests: %v", got)
	}
}

func TestFilterRPCGuestsPreservesInput(t *testing.T) {
	input := []RPCGuest{{ID: "a"}, {ID: "b"}}
	got := filterRPCGuests(input, map[string]bool{"a": true})
	if len(got) != 1 || got[0].ID != "b" || input[0].ID != "a" {
		t.Fatalf("filter changed input or selected excluded Guest: %v", got)
	}
}

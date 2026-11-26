package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
)

func TestFixedBenchmarkWaitsForIdleGuestWithoutReplanning(t *testing.T) {
	guest := RPCGuest{ID: "node-1", Endpoint: "127.0.0.1:12555", RPCDeviceKinds: []string{"CPU"}}
	placement := benchmarkPlacement{
		ready: true,
		guests: []RPCGuest{guest},
		selection: clusterplan.Selection{Guests: []clusterplan.Candidate{{ID: guest.ID}}},
	}
	calls := 0
	err := placement.waitForRPCIdle(context.Background(), func() []RPCGuest {
		calls++
		if calls <= 2 {
			return nil
		}
		return []RPCGuest{guest}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("expected 2 missing samples before authenticated tunnel recovery, got %d", calls)
	}
	if placement.guests[0].Endpoint != guest.Endpoint {
		t.Fatal("fixed endpoint must not be rewritten during wait")
	}
}

func TestFixedBenchmarkRejectsChangedGuestTopology(t *testing.T) {
	guest := RPCGuest{ID: "node-1", Endpoint: "127.0.0.1:12555", RPCDeviceKinds: []string{"CPU"}}
	placement := benchmarkPlacement{
		ready: true,
		guests: []RPCGuest{guest},
		selection: clusterplan.Selection{Guests: []clusterplan.Candidate{{ID: guest.ID}}},
	}
	changed := guest
	changed.Endpoint = "127.0.0.1:12556"
	err := placement.waitForRPCIdle(context.Background(), func() []RPCGuest { return []RPCGuest{changed} })
	if err == nil || err.Error() != "fixed cluster Guest topology changed" {
		t.Fatalf("changed authenticated relay must invalidate benchmark: %v", err)
	}
}

func TestFixedBenchmarkWaitRespectsCancellation(t *testing.T) {
	guest := RPCGuest{ID: "node-1", Endpoint: "127.0.0.1:12555", RPCDeviceKinds: []string{"CPU"}}
	placement := benchmarkPlacement{
		ready: true,
		guests: []RPCGuest{guest},
		selection: clusterplan.Selection{Guests: []clusterplan.Candidate{{ID: guest.ID}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	err := placement.waitForRPCIdle(ctx, func() []RPCGuest { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait should preserve context cancellation: %v", err)
	}
}

package clusterplan

import "testing"

func TestAdaptiveSelectPrefersMeasuredFastGuest(t *testing.T) {
	const gib = uint64(1 << 30)
	nodes := []Candidate{
		{ID: "slow", CapacityBytes: 8 * gib, Performance: Performance{NetworkMeasured: true, RoundTripMS: 70, MiBPerSecond: 4, TokensPerSecond: 2}},
		{ID: "fast", CapacityBytes: 8 * gib, Performance: Performance{NetworkMeasured: true, RoundTripMS: 2, MiBPerSecond: 130, TokensPerSecond: 18}},
	}
	result, err := Select(40, 6*gib, gib, nodes, "speed")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Guests) != 1 || result.Guests[0].ID != "fast" || len(result.Excluded) != 1 || result.Excluded[0] != "slow" {
		t.Fatalf("unexpected selection: %+v", result)
	}
}
func TestAdaptiveSelectIncludesAdditionalGuestForFit(t *testing.T) {
	const gib = uint64(1 << 30)
	nodes := []Candidate{{ID: "fast", CapacityBytes: 5 * gib}, {ID: "slow", CapacityBytes: 3 * gib}}
	result, err := Select(40, 6*gib, 0, nodes, "speed")
	if err != nil || len(result.Guests) != 2 {
		t.Fatalf("expected both nodes: %+v %v", result, err)
	}
}
func TestAdaptiveSelectCapacityKeepsAllGuests(t *testing.T) {
	const gib = uint64(1 << 30)
	nodes := []Candidate{{ID: "first", CapacityBytes: 8 * gib}, {ID: "second", CapacityBytes: 8 * gib}}
	result, err := Select(40, 6*gib, gib, nodes, "capacity")
	if err != nil || len(result.Guests) != 2 || result.Guests[0].ID != "first" {
		t.Fatalf("capacity mode changed: %+v %v", result, err)
	}
}
func TestAdaptiveSelectRejectsInvalidState(t *testing.T) {
	const gib = uint64(1 << 30)
	for _, tc := range []struct {
		nodes []Candidate
		mode  string
	}{
		{[]Candidate{{ID: "a", CapacityBytes: 0}}, "speed"},
		{[]Candidate{{ID: "a", CapacityBytes: gib}, {ID: "a", CapacityBytes: gib}}, "speed"},
		{[]Candidate{{ID: "a", CapacityBytes: gib}}, "other"},
		{[]Candidate{{ID: "a", CapacityBytes: gib}}, "speed"},
	} {
		if _, err := Select(40, 10*gib, 0, tc.nodes, tc.mode); err == nil {
			t.Fatalf("expected validation/fit failure for %+v", tc)
		}
	}
}

func TestMeasuredThroughputOutranksNetworkHeuristic(t *testing.T) {
	const gib = uint64(1 << 30)
	nodes := []Candidate{
		{ID: "measured", CapacityBytes: 8 * gib, Performance: Performance{TokensPerSecond: 30, NetworkMeasured: true, RoundTripMS: 18, MiBPerSecond: 20}},
		{ID: "unknown", CapacityBytes: 8 * gib, Performance: Performance{NetworkMeasured: true, RoundTripMS: 1, MiBPerSecond: 150}},
	}
	result, err := Select(40, 6*gib, gib, nodes, "speed")
	if err != nil {
		t.Fatal(err)
	}
	if result.Guests[0].ID != "measured" {
		t.Fatalf("observed inference throughput ignored: %+v", result)
	}
}

func TestSelectWithHostSpeedWeightsLayersWithinMemoryBounds(t *testing.T) {
	const gib = uint64(1 << 30)
	// Equal memory (6 GiB each), 40 blocks, 8 GiB model -> memory-only would be 20/20.
	// Host is 3x faster (30 vs 10 tok/s) -> target 30 local layers, within 6 GiB cap.
	nodes := []Candidate{{ID: "guest", CapacityBytes: 6 * gib, Performance: Performance{TokensPerSecond: 10}}}
	fasterHost, err := SelectWithHostSpeed(40, 8*gib, 6*gib, 30, nodes, "capacity")
	if err != nil {
		t.Fatal(err)
	}
	if fasterHost.LocalLayers <= 20 {
		t.Fatalf("expected faster Host to keep >20 local layers, got %d (%s)", fasterHost.LocalLayers, fasterHost.Reason)
	}
	// Guest is 3x faster (10 vs 30 tok/s) -> target 10 local layers, within Guest 6 GiB cap.
	nodes[0].Performance.TokensPerSecond = 30
	fasterGuest, err := SelectWithHostSpeed(40, 8*gib, 6*gib, 10, nodes, "capacity")
	if err != nil {
		t.Fatal(err)
	}
	if fasterGuest.LocalLayers >= 20 {
		t.Fatalf("expected faster Guest to receive >20 layers (local <20), got %d", fasterGuest.LocalLayers)
	}
	// Missing Host benchmark preserves exact memory-proportional split (20).
	noBench, err := SelectWithHostSpeed(40, 8*gib, 6*gib, 0, nodes, "capacity")
	if err != nil || noBench.LocalLayers != 20 {
		t.Fatalf("expected memory-proportional 20 layers without Host speed, got %+v err=%v", noBench, err)
	}
}

func TestEstimatedPlacementAvoidsHighRTTGuest(t *testing.T) {
	const gib = uint64(1 << 30)
	nodes := []Candidate{
		{ID: "fast-compute-slow-link", CapacityBytes: 6 * gib, Performance: Performance{TokensPerSecond: 30, RoundTripMS: 100, NetworkMeasured: true}},
		{ID: "fast-link", CapacityBytes: 6 * gib, Performance: Performance{TokensPerSecond: 20, RoundTripMS: 1, NetworkMeasured: true}},
	}
	got, err := SelectWithHostSpeedAndRTT(40, 8*gib, 6*gib, 15, nodes, "speed")
	if err != nil || len(got.Guests) != 1 || got.Guests[0].ID != "fast-link" || len(got.Excluded) != 1 {
		t.Fatalf("high RTT link was not excluded: %+v %v", got, err)
	}
	defaultPlan, err := SelectWithHostSpeed(40, 8*gib, 6*gib, 15, nodes, "speed")
	if err != nil || defaultPlan.Guests[0].ID != "fast-compute-slow-link" {
		t.Fatalf("RTT cost applied without explicit opt-in: %+v %v", defaultPlan, err)
	}
	legacy, err := SelectWithHostSpeedAndRTT(40, 8*gib, 6*gib, 0, nodes, "speed")
	if err != nil || legacy.LocalLayers != 20 || legacy.Guests[0].ID != "fast-compute-slow-link" {
		t.Fatalf("unmeasured Host changed legacy selection: %+v %v", legacy, err)
	}
}

func TestLayerCapacityAvoidsOverflow(t *testing.T) {
	const required = ^uint64(0) - 1
	if got := layerCapacity(40, required, required/2); got != 20 {
		t.Fatalf("overflow in memory clamp: %d", got)
	}
}

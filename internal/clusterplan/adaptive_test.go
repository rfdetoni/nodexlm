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
        {ID:"measured", CapacityBytes:8*gib,Performance:Performance{TokensPerSecond:30,NetworkMeasured:true,RoundTripMS:18,MiBPerSecond:20}},
        {ID:"unknown", CapacityBytes:8*gib,Performance:Performance{NetworkMeasured:true,RoundTripMS:1,MiBPerSecond:150}},
    }
    result, err := Select(40, 6*gib, gib, nodes, "speed")
    if err != nil { t.Fatal(err) }
    if result.Guests[0].ID != "measured" {t.Fatalf("observed inference throughput ignored: %+v",result)}
}

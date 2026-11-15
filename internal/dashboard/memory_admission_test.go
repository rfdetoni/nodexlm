package dashboard

import (
	"strings"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
)

func TestMemoryAdmissionAccountsForContextAndBatch(t *testing.T) {
	meta := local.GGUFInfo{BlockCount: 40, EmbeddingLength: 8192, HeadCount: 64, KVHeadCount: 16}
	settings := Settings{ContextTokens: 2048, BatchSize: 512, KVCacheType: "f16"}
	if err := validatePlacementMemory(meta, settings, 3*clusterplan.GiB, 8*clusterplan.GiB, nil); err != nil {
		t.Fatalf("regular context unexpectedly rejected: %v", err)
	}
	settings.ContextTokens = 262144
	if err := validatePlacementMemory(meta, settings, 3*clusterplan.GiB, 8*clusterplan.GiB, nil); err == nil ||
		!strings.Contains(err.Error(), "context/batch") {
		t.Fatalf("large KV allocations should be rejected: %v", err)
	}
	settings.ContextTokens = 2048
	settings.BatchSize = 8192
	if got := estimateTransientBytes(meta, settings); got == nil || *got <= 0 {
		t.Fatalf("batch memory estimate unavailable: %v", got)
	}
}

func TestMemoryAdmissionDoesNotInventMissingKVMetadata(t *testing.T) {
	meta := local.GGUFInfo{BlockCount: 40}
	settings := Settings{ContextTokens: 32768}
	if err := validatePlacementMemory(meta, settings, clusterplan.GiB, 8*clusterplan.GiB, nil); err == nil {
		t.Fatal("unknown high-context KV layout accepted without measurement")
	}
	settings.ContextTokens = 2048
	if err := validatePlacementMemory(meta, settings, clusterplan.GiB, 8*clusterplan.GiB, nil); err != nil {
		t.Fatalf("legacy ordinary context rejected: %v", err)
	}
}

func TestSpeedSelectionCanAddGuestsForKVAdmission(t *testing.T) {
	m, err := NewManager(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	guestCapacity := uint64(16) * clusterplan.GiB
	guests := []RPCGuest{{ID: "fast", CapacityBytes: guestCapacity}, {ID: "additional", CapacityBytes: guestCapacity}}
	meta := local.GGUFInfo{BlockCount: 40, EmbeddingLength: 8192, HeadCount: 64, KVHeadCount: 16}
	settings := Settings{ContextTokens: 81920, PlacementStrategy: "speed"}
	selection, err := m.chooseRPC(settings, meta, 2*clusterplan.GiB, 0, guests)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Guests) != 2 || len(selection.Excluded) != 0 {
		t.Fatalf("selection ignored KV context memory: %+v", selection)
	}
}

// Verify the same KV estimate is used by the memory planner and the preview.
func TestEstimatedKVBytesAreSharedAcrossMemoryChecks(t *testing.T) {
	meta := local.GGUFInfo{BlockCount: 40, EmbeddingLength: 8192, HeadCount: 64, KVHeadCount: 16}
	for _, tc := range []struct {
		cache string
		want  uint64
	}{
		{"f16", 671088640},
		{"q8_0", 356515840},
		{"q4_0", 188743680},
	} {
		t.Run(tc.cache, func(t *testing.T) {
			s := Settings{ContextTokens: 2048, KVCacheType: tc.cache, BatchSize: 512}
			got := estimateKVBytes(meta, s)
			if got == nil || *got != tc.want {
				t.Fatalf("KV estimate for %s = %v, want %d", tc.cache, got, tc.want)
			}
			transient := estimateTransientBytes(meta, s)
			activation := uint64(512) * uint64(meta.EmbeddingLength) * 4
			if transient == nil || *transient != tc.want+activation {
				t.Fatalf("transient estimate for %s = %v", tc.cache, transient)
			}
		})
	}
	if estimateKVBytes(local.GGUFInfo{}, Settings{ContextTokens: 2048}) != nil {
		t.Fatal("unknown KV metadata should remain unknown")
	}
	if estimateTransientBytes(meta, Settings{ContextTokens: 0}) != nil {
		t.Fatal("missing context should not be admitted")
	}
}

func TestModelExceedsBudgetPreservesExistingRounding(t *testing.T) {
	for _, tc := range []struct {
		bytes, budget uint64
		tooLarge      bool
	}{
		{800, 1000, false},
		{801, 1000, true},
		{1000, 1150, false},
		{1001, 1150, true},
		{1, 0, true},
	} {
		if got := modelExceedsBudget(tc.bytes, tc.budget); got != tc.tooLarge {
			t.Fatalf("model=%d budget=%d: got %v, want %v", tc.bytes, tc.budget, got, tc.tooLarge)
		}
	}
}

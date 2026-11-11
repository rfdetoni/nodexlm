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
	settings := Settings{ContextTokens: 131072, PlacementStrategy: "speed"}
	selection, err := m.chooseRPC(settings, meta, 2*clusterplan.GiB, 0, guests)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Guests) != 2 || len(selection.Excluded) != 0 {
		t.Fatalf("selection ignored KV context memory: %+v", selection)
	}
}

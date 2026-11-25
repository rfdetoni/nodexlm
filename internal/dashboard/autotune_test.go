package dashboard

import (
	"slices"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

func TestTuneCandidatesBoundedAndUnique(t *testing.T) {
	s := Settings{Threads: 4, ContextTokens: 2048, GPULayers: "auto"}
	rows := tuneCandidates(s, 8, false)
	if len(rows) < 2 || len(rows) > 7 || rows[0].Settings != s {
		t.Fatalf("invalid candidates: %+v", rows)
	}
	seen := map[Settings]bool{}
	for _, row := range rows {
		if seen[row.Settings] || row.Settings.Threads < 1 || row.Settings.Threads > 256 {
			t.Fatalf("invalid candidate: %+v", row)
		}
		seen[row.Settings] = true
	}
}

func TestBenchmarkClusterKeyDoesNotReplaceLiveSample(t *testing.T) {
	if got := benchmarkModel("model.gguf@bench-cluster"); got != "model.gguf" {
		t.Fatalf("incorrect cluster benchmark key: %s", got)
	}
}

func TestSlowRPCNgramCandidates(t *testing.T) {
	s := Settings{Threads: 4, ContextTokens: 2048, BatchSize: 256, UBatchSize: 256}
	windows := map[int]bool{}
	for _, row := range tuneCandidates(s, 8, true) {
		if row.Settings.SpeculativeMode == "ngram" {
			windows[row.Settings.SpeculativeTokens] = true
			if row.Settings.BatchSize < row.Settings.UBatchSize {
				t.Fatal("invalid ngram batch")
			}
		}
	}
	if len(windows) != 3 || !windows[4] || !windows[8] || !windows[16] {
		t.Fatalf("unexpected ngram candidates: %v", windows)
	}
}

func TestSpeculativeSuggestionRequiresMeasuredRemoteLayers(t *testing.T) {
	node := placement{NodeID: "guest", Layers: 10}
	for _, rtt := range []float64{0, 4.9, 5, 20} {
		node.Network = &rpcmesh.Measurement{RoundTripMS: rtt}
		got := speculativeSuggestion([]placement{node})
		if (rtt >= 5) != (len(got) == 1) {
			t.Fatalf("unexpected suggestion at RTT %v: %+v", rtt, got)
		}
	}
	node.Layers = 0
	if len(speculativeSuggestion([]placement{node})) != 0 {
		t.Fatal("suggestion for unused remote node")
	}
	node.Layers, node.Network = 10, nil
	if len(speculativeSuggestion([]placement{node})) != 0 {
		t.Fatal("suggestion without a network measurement")
	}
}

func TestPrefillCandidatesMatchMeasuredLink(t *testing.T) {
	for _, tc := range []struct {
		link string
		want []int
	}{{"slow", []int{128, 256}}, {"fast", []int{2048}}, {"", []int{512, 1024}}} {
		var got []int
		for _, row := range tuneCandidates(Settings{Threads: 4}, 8, false, tc.link) {
			if row.Name == "prefill" {
				got = append(got, row.Settings.UBatchSize)
				if row.Settings.BatchSize < row.Settings.UBatchSize {
					t.Fatal("batch smaller than ubatch")
				}
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.link, got, tc.want)
		}
	}
}

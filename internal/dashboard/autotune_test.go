package dashboard

import "testing"

func TestTuneCandidatesBoundedAndUnique(t *testing.T) {
	s := Settings{Threads: 4, ContextTokens: 2048, GPULayers: "auto"}
	rows := tuneCandidates(s, 8)
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

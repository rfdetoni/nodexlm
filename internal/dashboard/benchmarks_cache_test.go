package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBenchmarkMedianResistsOutlier(t *testing.T) {
	measured := []InferenceSample{
		{TokensPerSecond: 2, Seconds: 10},
		{TokensPerSecond: 24, Seconds: 1},
		{TokensPerSecond: 23, Seconds: 1.1},
	}
	got := medianInferenceSample(measured)
	if got.TokensPerSecond != 23 || got.Seconds != 1.1 {
		t.Fatalf("median should be one complete observed run: %+v", got)
	}
}

func TestBenchmarkCachePersistsOnlyMatchingModelAndOptions(t *testing.T) {
	dir, models := t.TempDir(), t.TempDir()
	path := filepath.Join(models, "small.gguf")
	if err := os.WriteFile(path, []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(dir, models, "")
	if err != nil {
		t.Fatal(err)
	}
	s := m.Settings()
	fingerprint := m.benchmarkSignature(s, "small.gguf")
	if fingerprint == "" {
		t.Fatal("missing model fingerprint")
	}
	sample := InferenceSample{Model: "small.gguf", Kind: "benchmark", Runs: 3,
		MeasuredAt: time.Now(), TokensPerSecond: 10, Fingerprint: fingerprint}
	m.mu.Lock()
	m.samples[sample.Model] = sample
	err = m.persistBenchmarksLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	m.Close()

	m, err = NewManager(dir, models, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.InferenceSamples(); len(got) != 1 || got[0].Runs != 3 {
		t.Fatalf("benchmarks were not restored: %+v", got)
	}
	m.Close()
	if err := os.WriteFile(path, []byte("GGUF changed"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err = NewManager(dir, models, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.InferenceSamples(); len(got) != 0 {
		t.Fatalf("changed model reused benchmark: %+v", got)
	}
	m.Close()

	if err := os.WriteFile(path, []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err = NewManager(dir, models, "")
	if err != nil {
		t.Fatal(err)
	}
	modified := s
	modified.BatchSize = 512
	if err := m.Update(modified); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = NewManager(dir, models, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if got := m.InferenceSamples(); len(got) != 0 {
		t.Fatalf("changed native options reused benchmark: %+v", got)
	}
}

func TestUnknownOrExpiredBenchmarksAreNotRestored(t *testing.T) {
	dir, models := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(models, "small.gguf"), []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(dir, models, "")
	if err != nil {
		t.Fatal(err)
	}
	fp := m.benchmarkSignature(m.Settings(), "small.gguf")
	m.Close()
	records := []InferenceSample{
		{Model: "small.gguf", Kind: "benchmark", TokensPerSecond: 9, Fingerprint: fp, MeasuredAt: time.Now().Add(-25 * time.Hour)},
		{Model: "missing.gguf", Kind: "benchmark", TokensPerSecond: 9, Fingerprint: fp, MeasuredAt: time.Now()},
		{Model: "small.gguf", Kind: "live", TokensPerSecond: 9, Fingerprint: fp, MeasuredAt: time.Now()},
	}
	body, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "benchmarks.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	m, err = NewManager(dir, models, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if len(m.InferenceSamples()) != 0 {
		t.Fatal("expired or invalid benchmarks restored")
	}
}

package dashboard

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
)

// Fingerprints tie persisted benchmarks to model bytes, native options and the
// bundled runtime revision. Missing or changed files invalidate the sample.
func (m *Manager) benchmarkSignature(s Settings, model string) string {
	path, err := modelPath(m.modelDir, model)
	if err != nil {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	s.Model, s.Distributed, s.PlacementStrategy = "", false, ""
	value, err := json.Marshal(struct {
		Options  Settings `json:"options"`
		Bytes    int64    `json:"bytes"`
		Modified int64    `json:"modified"`
		Runtime  string   `json:"runtime"`
		Bundle   string   `json:"bundle"`
		Schema   int      `json:"schema"`
	}{s, info.Size(), info.ModTime().UnixNano(), llamaruntime.Version, llamaruntime.BundledRPCRelease(), 2})
	if err != nil {
		return ""
	}
	sha := sha256.Sum256(value)
	return fmt.Sprintf("%x", sha[:])
}

func benchmarkModel(key string) string {
	if strings.HasSuffix(key, "@bench-cluster") {
		return strings.TrimSuffix(key, "@bench-cluster")
	}
	if at := strings.LastIndex(key, "@"); at > 0 && len(key)-at-1 == 64 {
		return key[:at]
	}
	return key
}

func (m *Manager) loadBenchmarks() {
	body, err := os.ReadFile(filepath.Join(m.dir, "benchmarks.json"))
	if err != nil {
		return
	}
	var records []InferenceSample
	if err := json.Unmarshal(body, &records); err != nil {
		slog.Warn("Ignoring invalid benchmark cache", "error", err)
		return
	}
	now := time.Now()
	for _, sample := range records {
		if len(m.samples) >= 32 {
			break
		}
		if sample.Kind != "benchmark" || sample.TokensPerSecond <= 0 ||
			sample.MeasuredAt.After(now) || now.Sub(sample.MeasuredAt) >= 24*time.Hour ||
			sample.Fingerprint == "" ||
			sample.Fingerprint != m.benchmarkSignature(m.settings, benchmarkModel(sample.Model)) {
			continue
		}
		m.samples[sample.Model] = sample
	}
}

// Requires m.mu. Only controlled, validated benchmarks are persisted;
// conversational contents and live telemetry are never written to disk.
func (m *Manager) persistBenchmarksLocked() error {
	records := make([]InferenceSample, 0, len(m.samples))
	now := time.Now()
	for _, sample := range m.samples {
		if sample.Kind == "benchmark" && sample.Fingerprint != "" &&
			!sample.MeasuredAt.After(now) && now.Sub(sample.MeasuredAt) < 24*time.Hour {
			records = append(records, sample)
		}
	}
	body, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(m.dir, "benchmarks.json"), body)
}

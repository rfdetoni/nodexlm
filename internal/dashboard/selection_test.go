package dashboard

import (
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/local"
)

func TestChooseRPCUsesMeasuredInference(t *testing.T) {
	const gib = uint64(1 << 30)
	m, err := NewManager(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	slow := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fast := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	m.samples["small.gguf@"+slow] = InferenceSample{Model: "small.gguf@" + slow, MeasuredAt: time.Now(), TokensPerSecond: 2}
	m.samples["small.gguf@"+fast] = InferenceSample{Model: "small.gguf@" + fast, MeasuredAt: time.Now(), TokensPerSecond: 14}
	guests := []RPCGuest{{ID: slow, CapacityBytes: 8 * gib}, {ID: fast, CapacityBytes: 8 * gib}}
	selection, err := m.chooseRPC(Settings{Model: "small.gguf", PlacementStrategy: "speed"}, local.GGUFInfo{BlockCount: 40}, 6*gib, gib, guests)
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Guests) != 1 || selection.Guests[0].ID != fast {
		t.Fatalf("did not prioritize the observed faster Guest: %+v", selection)
	}
}

func TestSettingsRejectUnsupportedNativeOptions(t *testing.T) {
	m, err := NewManager(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	valid := m.Settings()
	valid.Model = ""
	cases := []func(*Settings){
		func(s *Settings) { s.PlacementStrategy = "unknown" },
		func(s *Settings) { s.FlashAttention = "always" },
		func(s *Settings) { s.KVCacheType = "unsafe" },
		func(s *Settings) { s.BatchSize = 999999 },
		func(s *Settings) { s.BatchSize = 64; s.UBatchSize = 128 },
		func(s *Settings) { s.SpeculativeMode = "remote" },
	}
	for i, mutate := range cases {
		s := valid
		mutate(&s)
		if err := m.validate(s); err == nil {
			t.Fatalf("case %d accepted invalid native settings", i)
		}
	}
}

func TestUpdateInvalidatesMeasurementsAfterPerformanceSettingsChange(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	original := m.Settings()
	m.samples["old-model@node"] = InferenceSample{Model: "old-model@node", TokensPerSecond: 12, MeasuredAt: time.Now()}
	modified := original
	modified.BatchSize = 512
	if err := m.Update(modified); err != nil {
		t.Fatal(err)
	}
	if len(m.InferenceSamples()) != 0 {
		t.Fatal("stale benchmark was retained across tuning change")
	}
}

func TestExperimentalBackendSelectionAndGuard(t *testing.T) {
	m, err := NewManager(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	config := m.Settings()
	config.Backend = "unsupported"
	if err = m.Update(config); err == nil {
		t.Fatal("unsupported backend accepted")
	}
	config.Backend = "mistralrs"
	config.Distributed = true
	if err = m.Update(config); err == nil {
		t.Fatal("mistral.rs accepted as a NodexLM RPC Guest runtime")
	}
	config.Distributed = false
	if err = m.Update(config); err != nil {
		t.Fatal(err)
	}
	if got := m.State()["backend"]; got != "mistralrs" {
		t.Fatalf("configured backend not exposed: %v", got)
	}
	config.Backend = ""
	if err = m.Update(config); err != nil {
		t.Fatal(err)
	}
	if got := m.State()["backend"]; got != "llama.cpp" {
		t.Fatalf("backwards-compatible llama default lost: %v", got)
	}
}

func TestChooseRPCSpeedCanUseHostAlone(t *testing.T) {
	const gib = uint64(1 << 30)
	m, err := NewManager(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	node := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	settings := Settings{Model: "small.gguf", PlacementStrategy: "speed"}
	meta := local.GGUFInfo{BlockCount: 40}
	guest := []RPCGuest{{ID: node, CapacityBytes: 8 * gib}}
	hostOnly, err := m.chooseRPC(settings, meta, gib, 8*gib, guest)
	if err != nil || len(hostOnly.Guests) != 0 || hostOnly.LocalLayers != 40 ||
		len(hostOnly.Excluded) != 1 || hostOnly.Excluded[0] != node {
		t.Fatalf("Host-only speed option was not selected: %+v %v", hostOnly, err)
	}
	m.samples["small.gguf"] = InferenceSample{Model: "small.gguf", TokensPerSecond: 10, MeasuredAt: time.Now()}
	m.samples["small.gguf@"+node] = InferenceSample{Model: "small.gguf@" + node, TokensPerSecond: 30, MeasuredAt: time.Now()}
	remote, err := m.chooseRPC(settings, meta, gib, 8*gib, guest)
	if err != nil || len(remote.Guests) != 1 || remote.Guests[0].ID != node {
		t.Fatalf("faster measured Guest must remain eligible: %+v %v", remote, err)
	}
	m.samples["small.gguf"] = InferenceSample{Model: "small.gguf", TokensPerSecond: 40, MeasuredAt: time.Now()}
	hostOnly, err = m.chooseRPC(settings, meta, gib, 8*gib, guest)
	if err != nil || len(hostOnly.Guests) != 0 {
		t.Fatalf("faster measured Host must be eligible: %+v %v", hostOnly, err)
	}
	settings.PlacementStrategy = "capacity"
	required, err := m.chooseRPC(settings, meta, gib, 8*gib, guest)
	if err != nil || len(required.Guests) != 1 {
		t.Fatalf("explicit distributed capacity strategy must keep Guest: %+v %v", required, err)
	}
	settings.PlacementStrategy = "speed"
	noGuest, err := m.chooseRPC(settings, meta, gib, 8*gib, nil)
	if err != nil || len(noGuest.Guests) != 0 || noGuest.LocalLayers != 40 {
		t.Fatalf("Host-only must work without an RPC provider: %+v %v", noGuest, err)
	}
}

func TestChooseRPCSkipsGuestsWithoutUsableRAM(t *testing.T) {
	const gib = uint64(1 << 30)
	m, err := NewManager(t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	settings := Settings{Model: "small.gguf", PlacementStrategy: "capacity", ContextTokens: 2048}
	guests := []RPCGuest{
		{ID: "android-low-memory", CapacityBytes: 0},
		{ID: "linux-healthy", CapacityBytes: 8 * gib},
	}
	meta := local.GGUFInfo{BlockCount: 32}
	result, err := m.chooseRPC(settings, meta, 3*gib, gib, guests)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Guests) != 1 || result.Guests[0].ID != "linux-healthy" ||
		len(result.Excluded) != 1 || result.Excluded[0] != "android-low-memory" {
		t.Fatalf("unavailable Android blocked healthy Guest: %+v", result)
	}
	if _, err := m.chooseRPC(settings, meta, 3*gib, gib, guests[:1]); err == nil {
		t.Fatal("zero-memory Guest was admitted without a usable alternative")
	}
	if _, err := m.chooseRPC(settings, meta, 3*gib, gib,
		[]RPCGuest{{ID: "duplicated", CapacityBytes: 0}, {ID: "duplicated", CapacityBytes: 8 * gib}}); err == nil {
		t.Fatal("duplicate Guest ID must not become valid after filtering")
	}
}

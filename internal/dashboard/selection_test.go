package dashboard

import (
	"testing"
	"time"
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
	selection, err := m.chooseRPC(Settings{Model: "small.gguf", PlacementStrategy: "speed"}, 40, 6*gib, gib, guests)
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
	m,err:=NewManager(t.TempDir(),t.TempDir(),"")
	if err!=nil{t.Fatal(err)}
	defer m.Close()
	config:=m.Settings()
	config.Backend="unsupported"
	if err=m.Update(config);err==nil{t.Fatal("unsupported backend accepted")}
	config.Backend="mistralrs"
	config.Distributed=true
	if err=m.Update(config);err==nil{t.Fatal("mistral.rs accepted as a NodexLM RPC Guest runtime")}
	config.Distributed=false
	if err=m.Update(config);err!=nil{t.Fatal(err)}
	if got:=m.State()["backend"];got!="mistralrs" {
		t.Fatalf("configured backend not exposed: %v",got)
	}
	config.Backend=""
	if err=m.Update(config);err!=nil{t.Fatal(err)}
	if got:=m.State()["backend"];got!="llama.cpp" {
		t.Fatalf("backwards-compatible llama default lost: %v",got)
	}
}

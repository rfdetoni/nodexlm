package dashboard

import "testing"

func TestTrellisEffectiveSettingsDoNotChangeStockPreferences(t *testing.T) {
	original := Settings{Distributed: true, PrimaryNode: "guest", ParallelSlots: 4, SpeculativeMode: "ngram", SpeculativeTokens: 8, MoECacheMiB: 128, RPCNativeScheduler: true}
	got := trellisHostSettings(original)
	if got.Distributed || got.PrimaryNode != "" || got.ParallelSlots != 1 || got.SpeculativeMode != "off" || got.SpeculativeTokens != 0 || got.MoECacheMiB != 0 || got.RPCNativeScheduler {
		t.Fatalf("unsafe specialized placement: %+v", got)
	}
	if !original.Distributed || original.ParallelSlots != 4 || original.SpeculativeMode != "ngram" {
		t.Fatal("stock preferences were changed")
	}
}

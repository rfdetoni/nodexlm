package dashboard

// Runtime-specific settings are effective for this load only. The user's
// normal GGUF cluster preference remains unchanged for the next model.
func trellisHostSettings(s Settings) Settings {
	s.Distributed = false
	s.PrimaryNode = ""
	s.RPCNativeScheduler = false
	s.ParallelSlots = 1
	s.SpeculativeMode = "off"
	s.SpeculativeTokens = 0
	s.DraftModel = ""
	s.MoECacheMiB = 0
	s.MoECacheCPUShare = 0
	return s
}

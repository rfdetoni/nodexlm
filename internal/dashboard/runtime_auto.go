package dashboard

import (
	"errors"

	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/local"
)

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

// automaticModelSettings selects speed-first compatible placement for an
// explicitly selected model. Context preference is retained; model-specific
// options are reset rather than accidentally applied to unrelated weights.
func automaticModelSettings(previous Settings, model string) Settings {
	return Settings{
		Backend:           local.BackendLlama,
		Model:             model,
		Threads:           min(256, max(1, hardware.PhysicalCPUCount())),
		ContextTokens:     max(256, previous.ContextTokens),
		GPULayers:         "auto",
		HostGPU:           "auto",
		Distributed:       true,
		PlacementStrategy: "speed",
		ParallelSlots:     1,
		FlashAttention:    "auto",
		SpeculativeMode:   "off",
	}
}

func (m *Manager) ApplyAutomaticModel(model string) error {
	if model == "" {
		return errors.New("select a model")
	}
	s := automaticModelSettings(m.Settings(), model)
	path, err := modelPath(m.modelDir, model)
	if err != nil {
		return err
	}
	// Reject incompatible or incomplete weights before persisting settings.
	if _, err := local.ModelSize(path); err != nil {
		return err
	}
	if _, err := m.inspectInferenceGGUF(path, s); err != nil {
		return err
	}
	if err := m.Update(s); err != nil {
		return err
	}
	return m.StartLoad()
}

package dashboard

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/local"
)

// Resident models own independent native processes and KV caches. Loads are
// serialized against the primary runtime and conservatively budgeted against
// currently reported memory. No automatic eviction interrupts another client.
func (m *Manager) LoadModel(ctx context.Context, name string) error {
	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.mu.Lock()
	m.poolCancel = cancel
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.poolCancel = nil; m.mu.Unlock() }()
	m.mu.Lock()
	settings := m.settings
	if settings.VisionProjector != "" {
		m.mu.Unlock()
		return errors.New("additional residents cannot inherit a vision projector; clear the projector setting first")
	}
	// Pinned GGML RPC serves one client per Guest. Additional resident models
	// run on the Host rather than blocking on an already-owned native backend.
	settings.Distributed = false
	settings.PrimaryNode = ""
	if m.closed {
		m.mu.Unlock()
		return errors.New("manager closed")
	}
	if name == m.current || m.residents[name] != nil {
		m.mu.Unlock()
		return errors.New("model already resident")
	}
	count := len(m.residents)
	if m.engine != nil {
		count++
	}
	m.mu.Unlock()
	if count >= 4 {
		return errors.New("at most four models may be resident")
	}
	path, err := modelPath(m.modelDir, name)
	if err != nil {
		return err
	}
	size, err := local.ModelSize(path)
	if err != nil {
		return err
	}
	// OS-available memory already reflects loaded model allocations. Subtracting
	// the resident GGUF sizes again penalizes them twice. Use a fresh hardware
	// sample and the same context/batch admission guard as primary loads.
	settings.Model = name
	budget, err := m.hostBudget(settings, hardware.Collect("Host", nil))
	if err != nil {
		return err
	}
	if modelExceedsBudget(uint64(size), budget) {
		return errors.New("insufficient available memory for another resident model")
	}
	meta, err := local.InspectGGUF(path)
	if err != nil {
		return err
	}
	if err = validatePlacementMemory(meta, settings, uint64(size), budget, nil); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(name))
	child, err := NewManager(filepath.Join(m.dir, "resident", fmt.Sprintf("%x", sum[:12])), m.modelDir, m.binary)
	if err != nil {
		return err
	}
	settings.Model = name
	if err = child.Update(settings); err != nil {
		child.Close()
		return err
	}
	if err = child.Load(ctx); err != nil {
		child.Close()
		return err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		child.Close()
		return errors.New("manager closed")
	}
	m.residents[name] = child
	m.mu.Unlock()
	return nil
}
func (m *Manager) LoadedModels() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	models := []string{}
	if m.engine != nil {
		models = append(models, m.current)
	}
	for name := range m.residents {
		models = append(models, name)
	}
	sort.Strings(models)
	return models
}
func (m *Manager) HasModel(name string) bool {
	for _, v := range m.LoadedModels() {
		if v == name {
			return true
		}
	}
	return false
}
func (m *Manager) UnloadModel(name string) {
	m.poolMu.Lock()
	defer m.poolMu.Unlock()
	m.mu.Lock()
	child := m.residents[name]
	delete(m.residents, name)
	primary := name == m.current
	m.mu.Unlock()
	if child != nil {
		child.Close()
	}
	if primary {
		m.Unload()
	}
}

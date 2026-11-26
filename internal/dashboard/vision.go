package dashboard

// HasVision reports only the capabilities of the active resident runtime,
// not a saved setting or a filename heuristic.
func (m *Manager) HasVision(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.engine != nil && m.current == name && !m.unloading &&
		m.engine.VisionProjectorPath != "" && len(m.engine.RPCServers) == 0
}

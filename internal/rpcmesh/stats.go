package rpcmesh

type TunnelStats struct {
	Connected        int    `json:"connected"`
	Idle             int    `json:"idle"`
	Accepted         uint64 `json:"accepted"`
	CapacityRejected uint64 `json:"capacity_rejected"`
	WaitTimeouts     uint64 `json:"wait_timeouts"`
}

func (h *Hub) Stats() map[string]TunnelStats {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]TunnelStats{}
	for id, r := range h.nodes {
		r.mu.Lock()
		out[id] = TunnelStats{r.available, len(r.waiting), r.accepted, r.rejected, r.timeouts}
		r.mu.Unlock()
	}
	return out
}

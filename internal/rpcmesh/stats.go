package rpcmesh

type TunnelStats struct {
	Connected        int    `json:"connected"`
	Idle             int    `json:"idle"`
	Accepted         uint64 `json:"accepted"`
	CapacityRejected uint64 `json:"capacity_rejected"`
	WaitTimeouts     uint64 `json:"wait_timeouts"`
	BytesToGuest     uint64 `json:"bytes_to_guest"`
	BytesFromGuest   uint64 `json:"bytes_from_guest"`
	LastActivityMS   int64  `json:"last_activity_ms,omitempty"`
}

func (h *Hub) Stats() map[string]TunnelStats {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]TunnelStats{}
	for id, r := range h.nodes {
		r.mu.Lock()
		out[id] = TunnelStats{
			Connected: r.available, Idle: len(r.waiting),
			Accepted: r.accepted, CapacityRejected: r.rejected, WaitTimeouts: r.timeouts,
			BytesToGuest: r.bytesToGuest, BytesFromGuest: r.bytesFromGuest,
			LastActivityMS: r.lastActivityMS,
		}
		r.mu.Unlock()
	}
	return out
}

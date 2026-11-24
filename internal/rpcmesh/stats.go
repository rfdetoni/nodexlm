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
	WritesToGuest    uint64 `json:"writes_to_guest"`
	WritesFromGuest  uint64 `json:"writes_from_guest"`
	Epoch            uint64 `json:"-"`
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
			BytesToGuest: r.bytesToGuest.Load(), BytesFromGuest: r.bytesFromGuest.Load(),
			WritesToGuest: r.writesToGuest.Load(), WritesFromGuest: r.writesFromGuest.Load(),
			LastActivityMS: r.lastActivityMS.Load(), Epoch: r.epoch,
		}
		r.mu.Unlock()
	}
	return out
}

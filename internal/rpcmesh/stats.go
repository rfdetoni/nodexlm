package rpcmesh

type TunnelStats struct {
	Connected             int    `json:"connected"`
	Idle                  int    `json:"idle"`
	Accepted              uint64 `json:"accepted"`
	CapacityRejected      uint64 `json:"capacity_rejected"`
	WaitTimeouts          uint64 `json:"wait_timeouts"`
	BytesToGuest          uint64 `json:"bytes_to_guest"`
	BytesFromGuest        uint64 `json:"bytes_from_guest"`
	LastActivityMS        int64  `json:"last_activity_ms,omitempty"`
	WritesToGuest         uint64 `json:"writes_to_guest"`
	WritesFromGuest       uint64 `json:"writes_from_guest"`
	Epoch                 uint64 `json:"-"`
	FramedWireToGuest     uint64 `json:"framed_wire_bytes_to_guest,omitempty"`
	FramedWireFromGuest   uint64 `json:"framed_wire_bytes_from_guest,omitempty"`
	FramedNativeToGuest   uint64 `json:"framed_native_bytes_to_guest,omitempty"`
	FramedNativeFromGuest uint64 `json:"framed_native_bytes_from_guest,omitempty"`
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
			FramedWireToGuest: r.framedWireToGuest.Load(), FramedWireFromGuest: r.framedWireFromGuest.Load(),
			FramedNativeToGuest: r.framedNativeToGuest.Load(), FramedNativeFromGuest: r.framedNativeFromGuest.Load(),
		}
		r.mu.Unlock()
	}
	return out
}

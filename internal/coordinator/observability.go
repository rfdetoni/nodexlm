package coordinator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

type NodeObservation struct {
	ReconnectFailures uint64                `json:"reconnect_failures"`
	NodeID            string                `json:"node_id"`
	Latency           []rpcmesh.Measurement `json:"latency"`
	Tunnels           rpcmesh.TunnelStats   `json:"tunnels"`
}

func (s *Server) Observations() []NodeObservation {
	stats := s.rpcHub.Stats()
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []NodeObservation{}
	for id, history := range s.measurements {
		if s.nodes[id] == nil {
			continue
		}
		result = append(result, NodeObservation{NodeID: id, Latency: append([]rpcmesh.Measurement(nil), history...), Tunnels: stats[id], ReconnectFailures: s.nodes[id].device.Runtime.ReconnectFailures})
	}
	for id, stat := range stats {
		if s.nodes[id] == nil {
			continue
		}
		if s.measurements[id] == nil {
			result = append(result, NodeObservation{NodeID: id, Latency: []rpcmesh.Measurement{}, Tunnels: stat, ReconnectFailures: s.nodes[id].device.Runtime.ReconnectFailures})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].NodeID < result[j].NodeID })
	return result
}
func (s *Server) saveMeasurement(m rpcmesh.Measurement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := append(s.measurements[m.NodeID], m)
	if len(history) > 60 {
		history = history[len(history)-60:]
	}
	s.measurements[m.NodeID] = history
	if s.storePath == "" {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.storePath)
	f, err := os.CreateTemp(dir, ".bench-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(dir, "bench-"+m.NodeID+".json"))
}
func (s *Server) RecentMeasurement(id string) (rpcmesh.Measurement, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.measurements[id]
	if len(history) == 0 {
		return rpcmesh.Measurement{}, false
	}
	m := history[len(history)-1]
	return m, time.Since(m.MeasuredAt) < 24*time.Hour
}

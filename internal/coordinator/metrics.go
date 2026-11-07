package coordinator

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

func (s *Server) Metrics(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	s.WriteMetrics(w)
}
func (s *Server) WriteMetrics(w http.ResponseWriter) {
	nodes := s.DevicesSnapshot()
	online := 0
	for _, n := range nodes {
		if n.Status == "online" {
			online++
		}
	}
	s.mu.Lock()
	counts := map[string]int{"queued": 0, "running": 0, "completed": 0, "failed": 0, "cancelled": 0}
	for _, j := range s.jobs {
		counts[j.Status]++
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# TYPE nodexlm_nodes_online gauge\nnodexlm_nodes_online %d\n# TYPE nodexlm_rpc_nodes gauge\nnodexlm_rpc_nodes %d\n# TYPE nodexlm_jobs gauge\n", online, len(s.RPCNodes()))
	for _, state := range []string{"queued", "running", "completed", "failed", "cancelled"} {
		fmt.Fprintf(w, "nodexlm_jobs{status=%q} %d\n", state, counts[state])
	}
}
func (s *Server) Benchmark(ctx context.Context, id string) (rpcmesh.Measurement, error) {
	return s.rpcHub.Benchmark(ctx, id)
}
func (s *Server) bench(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	n := s.nodes[id]
	ready := n != nil && n.device.Runtime.Benchmark
	busy := n != nil && n.device.Busy
	s.mu.Unlock()
	if !ready {
		fail(w, 409, "Guest does not advertise benchmark support; upgrade the Guest")
		return
	}
	if busy {
		fail(w, 409, "Guest has an active job")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, err := s.Benchmark(ctx, id)
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	respond(w, 200, out)
}

// Model IDs for the queued-worker API. Host-native models are served by the dashboard.
func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	names := map[string]bool{}
	for _, d := range s.DevicesSnapshot() {
		if d.Status == "online" {
			for _, m := range d.Models {
				if m != "*" {
					names[m] = true
				}
			}
		}
	}
	ids := make([]string, 0, len(names))
	for m := range names {
		ids = append(ids, m)
	}
	sort.Strings(ids)
	out := []any{}
	for _, m := range ids {
		out = append(out, map[string]any{"id": m, "object": "model", "created": 0, "owned_by": "nodexlm"})
	}
	respond(w, 200, map[string]any{"object": "list", "data": out})
}

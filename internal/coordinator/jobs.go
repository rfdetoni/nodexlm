package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"net/http"
	"time"
)

type jobEvents struct {
	changed chan struct{}
	frames  []json.RawMessage
	bytes   int
}

func (s *Server) notifyLocked(id string) {
	if e := s.events[id]; e != nil {
		select {
		case e.changed <- struct{}{}:
		default:
		}
	}
}
func (s *Server) cancelLocked(id, reason string) {
	j := s.jobs[id]
	if j == nil || j.Status != "queued" && j.Status != "running" {
		return
	}
	now := time.Now()
	j.Status = "cancelled"
	j.Error = reason
	j.Completed = &now
	// Keep the node busy until it acknowledges cancellation or its lease expires;
	// otherwise another generation could be sent to the same native engine.
	if j.NodeID == "" {
		for i, q := range s.queue {
			if q == id {
				s.queue = append(s.queue[:i], s.queue[i+1:]...)
				break
			}
		}
	}
	s.notifyLocked(id)
}
func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := r.PathValue("id")
	if s.jobs[id] == nil {
		fail(w, 404, "job not found")
		return
	}
	s.cancelLocked(id, "cancelled by client")
	respond(w, 200, s.jobs[id])
}
func (s *Server) nodeJob(w http.ResponseWriter, r *http.Request) {
	_, id := s.identify(r)
	if id == "" {
		fail(w, 401, "unauthorized")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[r.PathValue("id")]
	if j == nil || j.NodeID != id {
		fail(w, 404, "job not found")
		return
	}
	respond(w, 200, map[string]string{"status": j.Status})
}
func (s *Server) streamChunk(w http.ResponseWriter, r *http.Request) {
	_, id := s.identify(r)
	if id == "" {
		fail(w, 401, "unauthorized")
		return
	}
	var in struct {
		Data json.RawMessage `json:"data"`
	}
	if !parse(w, r, &in) {
		return
	}
	if len(in.Data) == 0 || len(in.Data) > 65536 {
		fail(w, 400, "invalid stream event")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jid := r.PathValue("id")
	j := s.jobs[jid]
	e := s.events[jid]
	n := s.nodes[id]
	if j == nil || n == nil || j.NodeID != id || n.active != jid || j.Status != "running" || !j.Request.Stream || e == nil {
		fail(w, 409, "job not streaming on this node")
		return
	}
	if e.bytes+len(in.Data) > maxBody/2 || len(e.frames) >= 128 {
		fail(w, 429, "stream buffer exceeded")
		return
	}
	e.bytes += len(in.Data)
	e.frames = append(e.frames, in.Data)
	s.notifyLocked(jid)
	w.WriteHeader(204)
}

func (s *Server) waitChat(w http.ResponseWriter, r *http.Request, id string, stream bool) {
	defer func() { s.mu.Lock(); s.cancelLocked(id, "client disconnected or deadline exceeded"); s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(r.Context(), jobDeadline+20*time.Second)
	defer cancel()
	started := false
	for {
		s.mu.Lock()
		j := *s.jobs[id]
		events := s.events[id]
		frames := events.frames
		events.frames = nil
		changed := events.changed
		s.mu.Unlock()
		for _, frame := range frames {
			if !started {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Accel-Buffering", "no")
				started = true
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := fmt.Fprintf(w, "data: %s\n\n", frame); err != nil {
				return
			}
			if http.NewResponseController(w).Flush() != nil {
				return
			}
		}
		if j.Status == "completed" {
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				_ = http.NewResponseController(w).Flush()
			} else {
				respond(w, 200, map[string]any{"id": j.ID, "object": "chat.completion", "created": j.Created.Unix(), "model": j.Request.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": j.Result}, "finish_reason": "stop"}}})
			}
			return
		}
		if j.Status == "failed" || j.Status == "cancelled" {
			if started {
				raw, _ := json.Marshal(map[string]string{"error": j.Error})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
				_ = http.NewResponseController(w).Flush()
			} else {
				fail(w, 502, j.Error)
			}
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			if !started && r.Context().Err() == nil {
				fail(w, 504, "inference timed out")
			}
			return
		}
	}
}

// QueueChat is called after the loopback dashboard has authenticated either
// an admin or its inference-only API credential.
func (s *Server) QueueChat(w http.ResponseWriter, r *http.Request, req protocol.ChatRequest) {
	job, err := s.queueJob(req)
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	s.waitChat(w, r, job.ID, req.Stream)
}

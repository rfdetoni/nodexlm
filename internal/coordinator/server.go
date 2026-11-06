// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/pairing"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

const (
	maxBody        = 1 << 20
	leaseWait      = 20 * time.Second
	heartbeatGrace = 65 * time.Second
	jobDeadline    = 4 * time.Minute
)

type nodeState struct {
	device     protocol.Device
	credential [32]byte
	signal     chan struct{}
	active     string
}

type Server struct {
	mu         sync.Mutex
	admin      [32]byte
	enrollment [32]byte
	nodes      map[string]*nodeState
	jobs       map[string]*protocol.Job
	queue      []string
	logger     *slog.Logger
	storePath  string
	pair       *pairing.Session
	certDER    []byte
	rpcHub     *rpcmesh.Hub
}

func New(adminToken, enrollmentToken string) (*Server, error) {
	if len(adminToken) < 32 || len(enrollmentToken) < 32 || adminToken == enrollmentToken {
		return nil, errors.New("admin and enrollment tokens must be distinct, at least 32 characters each")
	}
	return &Server{admin: sha256.Sum256([]byte(adminToken)), enrollment: sha256.Sum256([]byte(enrollmentToken)), nodes: map[string]*nodeState{}, jobs: map[string]*protocol.Job{}, logger: slog.Default(), rpcHub: rpcmesh.NewHub()}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/nodes/enroll", s.enroll)
	mux.HandleFunc("GET /v1/pairing/offer", s.pairingOffer)
	mux.HandleFunc("POST /v1/pairing/start", s.pairingStart)
	mux.HandleFunc("POST /v1/pairing/enroll", s.pairingEnroll)
	mux.HandleFunc("POST /v1/nodes/heartbeat", s.heartbeat)
	mux.HandleFunc("GET /v1/nodes/catalog", s.modelCatalog)
	mux.HandleFunc("POST /v1/nodes/lease", s.lease)
	mux.HandleFunc("CONNECT /v1/nodes/rpc/tunnel", s.rpcTunnel)
	mux.HandleFunc("POST /v1/nodes/result/{id}", s.result)
	mux.HandleFunc("GET /v1/nodes", s.listNodes)
	mux.HandleFunc("DELETE /v1/nodes/{id}", s.revokeNode)
	mux.HandleFunc("POST /v1/jobs", s.submit)
	mux.HandleFunc("GET /v1/jobs/{id}", s.getJob)
	mux.HandleFunc("POST /v1/chat/completions", s.chat)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" {
			r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		}
		mux.ServeHTTP(w, r)
	})
}

func auth(r *http.Request, sum [32]byte) bool {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return false
	}
	candidate := sha256.Sum256([]byte(strings.TrimPrefix(v, "Bearer ")))
	return subtle.ConstantTimeCompare(candidate[:], sum[:]) == 1
}

func (s *Server) adminOK(w http.ResponseWriter, r *http.Request) bool {
	if auth(r, s.admin) {
		return true
	}
	fail(w, http.StatusUnauthorized, "unauthorized")
	return false
}

func parse(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON or body too large")
		return false
	}
	return true
}

func respond(w http.ResponseWriter, status int, obj any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(obj)
}
func fail(w http.ResponseWriter, status int, msg string) {
	respond(w, status, map[string]string{"error": msg})
}

func token() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	if !auth(r, s.enrollment) {
		fail(w, 401, "invalid join credential")
		return
	}
	var req protocol.Enrollment
	if !parse(w, r, &req) {
		return
	}
	if !validDevice(req.Device) {
		fail(w, 400, "invalid node profile")
		return
	}
	s.register(w, req.Device)
}

func validDevice(d protocol.Device) bool {
	if len(d.Name) == 0 || len(d.Name) > 100 || len(d.Platform) == 0 || len(d.Platform) > 30 || len(d.Models) > 128 {
		return false
	}
	for _, m := range d.Models {
		if len(m) < 1 || len(m) > 256 {
			return false
		}
	}
	return true
}

func (s *Server) register(w http.ResponseWriter, d protocol.Device) {
	id, err := token()
	if err != nil {
		fail(w, 500, "identity generation failed")
		return
	}
	secret, err := token()
	if err != nil {
		fail(w, 500, "identity generation failed")
		return
	}
	d.ID = id
	d.LastSeen = time.Now()
	d.Status = "online"
	d.Busy = false
	s.mu.Lock()
	s.nodes[id] = &nodeState{device: d, credential: sha256.Sum256([]byte(secret)), signal: make(chan struct{}, 1)}
	if err := s.saveLocked(); err != nil {
		delete(s.nodes, id)
		s.mu.Unlock()
		fail(w, 500, "could not persist node")
		return
	}
	s.mu.Unlock()
	respond(w, 201, protocol.Credential{NodeID: id, Token: secret})
}

func (s *Server) identify(r *http.Request) (*nodeState, string) {
	id := r.Header.Get("X-Nodex-Node")
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.nodes[id]
	if n == nil || !auth(r, n.credential) {
		return nil, ""
	}
	return n, id
}

func (s *Server) lease(w http.ResponseWriter, r *http.Request) {
	n, id := s.identify(r)
	if n == nil {
		fail(w, 401, "unauthorized")
		return
	}
	var req struct {
		Models            []string `json:"models"`
		RAMBytes          uint64   `json:"ram_bytes"`
		AvailableRAMBytes uint64   `json:"available_ram_bytes"`
	}
	if !parse(w, r, &req) {
		return
	}
	if len(req.Models) > 128 {
		fail(w, 400, "too many models")
		return
	}
	deadline := time.NewTimer(leaseWait)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		n.device.LastSeen = time.Now()
		n.device.Status = "online"
		n.device.Models = append([]string(nil), req.Models...)
		if req.RAMBytes > 0 && req.RAMBytes < 1<<50 && req.AvailableRAMBytes <= req.RAMBytes {
			n.device.RAMBytes = req.RAMBytes
			n.device.AvailableRAMBytes = req.AvailableRAMBytes
		}
		if n.active == "" {
			for i, jid := range s.queue {
				j := s.jobs[jid]
				if j == nil || j.Status != "queued" {
					continue
				}
				if j.Request.TargetNode != "" && j.Request.TargetNode != id {
					continue
				}
				if !modelAvailable(req.Models, j.Request.Model) {
					continue
				}
				s.queue = append(s.queue[:i], s.queue[i+1:]...)
				now := time.Now()
				j.Status = "running"
				j.NodeID = id
				j.Started = &now
				n.active = jid
				n.device.Busy = true
				result := *j
				s.mu.Unlock()
				respond(w, 200, result)
				return
			}
		}
		ch := n.signal
		s.mu.Unlock()
		select {
		case <-ch:
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func modelAvailable(models []string, name string) bool {
	for _, v := range models {
		if v == name || v == "*" {
			return true
		}
	}
	return false
}

func (s *Server) result(w http.ResponseWriter, r *http.Request) {
	_, id := s.identify(r)
	if id == "" {
		fail(w, 401, "unauthorized")
		return
	}
	var out protocol.JobResult
	if !parse(w, r, &out) {
		return
	}
	if len(out.Result) > maxBody/2 || len(out.Error) > 1024 {
		fail(w, 400, "result too large")
		return
	}
	jid := r.PathValue("id")
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[jid]
	n := s.nodes[id]
	if j == nil || j.NodeID != id || j.Status != "running" || n.active != jid {
		fail(w, 409, "job is not leased to node")
		return
	}
	now := time.Now()
	j.Completed = &now
	j.Result = out.Result
	j.Error = out.Error
	if out.Error != "" {
		j.Status = "failed"
	} else {
		j.Status = "completed"
	}
	n.active = ""
	n.device.Busy = false
	respond(w, 200, map[string]string{"status": j.Status})
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.Device, 0, len(s.nodes))
	now := time.Now()
	for _, n := range s.nodes {
		d := n.device
		if now.Sub(d.LastSeen) > heartbeatGrace {
			d.Status = "offline"
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	respond(w, 200, out)
}

func validateChat(req protocol.ChatRequest) string {
	if req.Model == "" || len(req.Model) > 256 {
		return "model is required"
	}
	if len(req.Messages) == 0 || len(req.Messages) > 64 {
		return "1-64 messages required"
	}
	if req.MaxTokens < 0 || req.MaxTokens > 4096 {
		return "max_tokens exceeds limit"
	}
	if req.Stream {
		return "streaming is not implemented yet"
	}
	for _, m := range req.Messages {
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" {
			return "invalid message role"
		}
		if len(m.Content) > 65536 {
			return "message too large"
		}
	}
	return ""
}

func (s *Server) queueJob(req protocol.ChatRequest) (*protocol.Job, error) {
	id, err := token()
	if err != nil {
		return nil, err
	}
	job := &protocol.Job{ID: id, Request: req, Status: "queued", Created: time.Now()}
	s.mu.Lock()
	if len(s.queue) >= 128 {
		s.mu.Unlock()
		return nil, errors.New("queue at capacity")
	}
	s.jobs[id] = job
	s.queue = append(s.queue, id)
	for _, n := range s.nodes {
		select {
		case n.signal <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
	return job, nil
}
func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	var req protocol.ChatRequest
	if !parse(w, r, &req) {
		return
	}
	if reason := validateChat(req); reason != "" {
		fail(w, 400, reason)
		return
	}
	j, err := s.queueJob(req)
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	respond(w, 202, j)
}
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	s.mu.Lock()
	j := s.jobs[r.PathValue("id")]
	var out protocol.Job
	if j != nil {
		out = *j
	}
	s.mu.Unlock()
	if j == nil {
		fail(w, 404, "job not found")
		return
	}
	respond(w, 200, out)
}
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(w, r) {
		return
	}
	var req protocol.ChatRequest
	if !parse(w, r, &req) {
		return
	}
	if reason := validateChat(req); reason != "" {
		fail(w, 400, reason)
		return
	}
	j, err := s.queueJob(req)
	if err != nil {
		fail(w, 503, err.Error())
		return
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	max := time.NewTimer(jobDeadline + 20*time.Second)
	defer max.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-max.C:
			fail(w, 504, "inference timed out")
			return
		case <-tick.C:
			s.mu.Lock()
			snap := *s.jobs[j.ID]
			s.mu.Unlock()
			if snap.Status == "completed" {
				respond(w, 200, map[string]any{"id": snap.ID, "object": "chat.completion", "model": req.Model, "choices": []any{map[string]any{"index": 0, "message": protocol.Message{Role: "assistant", Content: snap.Result}, "finish_reason": "stop"}}})
				return
			}
			if snap.Status == "failed" {
				fail(w, 502, snap.Error)
				return
			}
		}
	}
}

// Cleanup cancels timed-out jobs and releases busy nodes. It does not retry an
// uncertain generation automatically, which could run the same job twice.
func (s *Server) Cleanup(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for _, j := range s.jobs {
				if j.Status == "running" && j.Started != nil && now.Sub(*j.Started) > jobDeadline {
					j.Status = "failed"
					j.Error = "execution lease expired"
					j.Completed = &now
					if n := s.nodes[j.NodeID]; n != nil && n.active == j.ID {
						n.active = ""
						n.device.Busy = false
					}
				}
				if j.Status == "queued" && now.Sub(j.Created) > jobDeadline {
					j.Status = "failed"
					j.Error = "no compatible node available"
					j.Completed = &now
				}
				if (j.Status == "completed" || j.Status == "failed") && j.Completed != nil && now.Sub(*j.Completed) > time.Hour {
					delete(s.jobs, j.ID)
				}
			}
			q := s.queue[:0]
			for _, id := range s.queue {
				if j := s.jobs[id]; j != nil && j.Status == "queued" {
					q = append(q, id)
				}
			}
			s.queue = q
			s.mu.Unlock()
		}
	}
}

// RPCEndpoints returns the loopback-only RPC endpoints whose Guests have
// active authenticated connections. Each endpoint represents one remote node.
func (s *Server) RPCEndpoints() []string      { return s.rpcHub.Endpoints() }
func (s *Server) RPCNodes() map[string]string { return s.rpcHub.ReadyNodes() }
func (s *Server) CloseRPC()                   { s.rpcHub.Close() }
func (s *Server) rpcTunnel(w http.ResponseWriter, r *http.Request) {
	_, id := s.identify(r)
	if id == "" {
		fail(w, 401, "invalid node credential")
		return
	}
	s.mu.Lock()
	if n := s.nodes[id]; n != nil {
		n.device.Runtime.RPCReady = true
		n.device.LastSeen = time.Now()
		n.device.Status = "online"
	}
	s.mu.Unlock()
	s.rpcHub.ServeTunnel(w, r, id)
}

// heartbeat retains the authenticated node identity while updating live
// resources. A node is not allowed to advertise itself as another node.
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	_, id := s.identify(r)
	if id == "" {
		fail(w, 401, "unauthorized")
		return
	}
	var profile protocol.Device
	if !parse(w, r, &profile) {
		return
	}
	if !validDevice(profile) || len(profile.GPUs) > 16 || profile.CPUCount < 1 || profile.CPUCount > 4096 {
		fail(w, 400, "invalid node profile")
		return
	}
	s.mu.Lock()
	n := s.nodes[id]
	if n == nil {
		s.mu.Unlock()
		fail(w, 401, "unauthorized")
		return
	}
	profile.ID = id
	profile.LastSeen = time.Now()
	profile.Status = "online"
	profile.Busy = n.device.Busy
	// RPCReady is asserted only when a tunnel is authenticated, not by a guest.
	profile.Runtime.RPCReady = n.device.Runtime.RPCReady
	n.device = profile
	s.mu.Unlock()
	respond(w, 200, map[string]string{"status": "online"})
}

// DevicesSnapshot returns an immutable copy for the localhost dashboard.
func (s *Server) DevicesSnapshot() []protocol.Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.Device, 0, len(s.nodes))
	for _, n := range s.nodes {
		d := n.device
		d.GPUs = append([]protocol.GPUDevice(nil), d.GPUs...)
		d.Models = append([]string(nil), d.Models...)
		if time.Since(d.LastSeen) > heartbeatGrace {
			d.Status = "offline"
		}
		out = append(out, d)
	}
	return out
}

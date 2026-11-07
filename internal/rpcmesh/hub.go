// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
// Package rpcmesh bridges unauthenticated ggml RPC over authenticated HTTPS
// tunnels. The native RPC server and the proxy listeners bind to loopback only.
package rpcmesh

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"
)

// idleTunnel owns its idle reader until a Host-side RPC connection claims it.
// The Guest cannot send application data before receiving the relay marker.
type idleTunnel struct {
	conn    net.Conn
	claimed chan struct{}
	stopped chan struct{}
}

type relay struct {
	listener  net.Listener
	waiting   []*idleTunnel
	ready     chan struct{}
	closed    chan struct{}
	mu        sync.Mutex
	available int
	peers     map[net.Conn]struct{}
}

type Hub struct {
	mu     sync.Mutex
	nodes  map[string]*relay
	closed bool
}

func NewHub() *Hub { return &Hub{nodes: make(map[string]*relay)} }

// ServeTunnel must only be called after checking the Guest's node credential.
// HTTP/1.1 CONNECT is hijacked so the TLS connection becomes a raw binary path.
func (h *Hub) ServeTunnel(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "HTTP/1.1 required", http.StatusHTTPVersionNotSupported)
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		http.Error(w, "shutting down", 503)
		return
	}
	item := h.nodes[nodeID]
	if item == nil {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			h.mu.Unlock()
			http.Error(w, "local RPC relay unavailable", 503)
			return
		}
		item = &relay{listener: ln, waiting: make([]*idleTunnel, 0, 8), ready: make(chan struct{}, 1), closed: make(chan struct{}), peers: make(map[net.Conn]struct{})}
		h.nodes[nodeID] = item
		go item.accept()
	}
	h.mu.Unlock()
	item.mu.Lock()
	if item.available >= 8 {
		item.mu.Unlock()
		slog.Warn("RPC tunnel capacity reached", "node", nodeID, "capacity", 8)
		http.Error(w, "RPC tunnel capacity reached", 503)
		return
	}
	item.available++
	item.mu.Unlock()
	conn, buf, err := hj.Hijack()
	if err != nil {
		item.decrement()
		return
	}
	item.track(conn)
	_ = conn.SetDeadline(time.Time{})
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	// The buffered writer owns HTTP headers; do not pass the socket to ggml until
	// the relay sends a one-byte ready signal when a local RPC connection arrives.
	if _, err = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err == nil {
		err = buf.Flush()
	}
	if err != nil {
		item.finish(conn)
		return
	}
	_ = conn.SetWriteDeadline(time.Time{})
	pending := &idleTunnel{
		conn:    conn,
		claimed: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	if item.enqueue(pending) {
		// Hijacking transfers ownership to the relay. The request context
		// cannot track this connection after ServeTunnel returns.
		go item.watchIdle(pending)
	}
}

// watchIdle detects a Guest that disconnected before being assigned to a
// GGML session. Only this goroutine reads from the idle socket; accept() stops
// it before writing the ready marker and starting the bidirectional bridge.
func (r *relay) watchIdle(t *idleTunnel) {
	defer close(t.stopped)
	var b [1]byte
	for {
		select {
		case <-t.claimed:
			return
		case <-r.closed:
			return
		default:
		}
		if err := t.conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			r.finish(t.conn)
			return
		}
		n, err := t.conn.Read(b[:])
		if n != 0 {
			slog.Warn("RPC Guest sent data before relay ready marker")
			r.finish(t.conn)
			return
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			continue
		}
		if err != nil {
			r.finish(t.conn)
			return
		}
	}
}
func (r *relay) signalReady() {
	select {
	case r.ready <- struct{}{}:
	default:
	}
}
func (r *relay) enqueue(t *idleTunnel) bool {
	r.mu.Lock()
	select {
	case <-r.closed:
		r.mu.Unlock()
		r.finish(t.conn)
		return false
	default:
	}
	r.waiting = append(r.waiting, t)
	r.mu.Unlock()
	r.signalReady()
	return true
}
func (r *relay) decrement() { r.mu.Lock(); r.available--; r.mu.Unlock() }
func (r *relay) track(conn net.Conn) {
	r.mu.Lock()
	r.peers[conn] = struct{}{}
	r.mu.Unlock()
}
func (r *relay) finish(conn net.Conn) {
	r.mu.Lock()
	if _, ok := r.peers[conn]; ok {
		delete(r.peers, conn)
		r.available--
	}
	for i, t := range r.waiting {
		if t.conn == conn {
			copy(r.waiting[i:], r.waiting[i+1:])
			r.waiting[len(r.waiting)-1] = nil
			r.waiting = r.waiting[:len(r.waiting)-1]
			break
		}
	}
	r.mu.Unlock()
	_ = conn.Close()
}
func (r *relay) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.available
}
func (r *relay) idleCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.waiting)
}
func (r *relay) takeIdle() net.Conn {
	r.mu.Lock()
	if len(r.waiting) == 0 {
		r.mu.Unlock()
		return nil
	}
	pending := r.waiting[0]
	r.waiting[0] = nil
	r.waiting = r.waiting[1:]
	close(pending.claimed)
	more := len(r.waiting) > 0
	r.mu.Unlock()
	if more {
		r.signalReady()
	}
	// Only the idle watcher reads from the socket. Stop it before giving
	// ownership to bridge(), otherwise the first RPC byte can be consumed.
	_ = pending.conn.SetReadDeadline(time.Now())
	<-pending.stopped
	_ = pending.conn.SetReadDeadline(time.Time{})
	r.mu.Lock()
	_, connected := r.peers[pending.conn]
	r.mu.Unlock()
	if !connected {
		return nil
	}
	return pending.conn
}
func (r *relay) accept() {
	for {
		upstream, err := r.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			// A worker that is not connected must never stall model load indefinitely.
			var guest net.Conn
			timeout := time.NewTimer(8 * time.Second)
			defer timeout.Stop()
			// Idle clients that disconnected are reclaimed before capacity
			// checks; wake promptly when a healthy Guest reconnects.
			for guest == nil {
				guest = r.takeIdle()
				if guest != nil {
					break
				}
				select {
				case <-r.ready:
				case <-timeout.C:
					slog.Warn("RPC relay timed out waiting for an idle Guest tunnel",
						"endpoint", r.listener.Addr().String(),
						"connected_tunnels", r.count(), "idle_tunnels", r.idleCount())
					_ = upstream.Close()
					return
				case <-r.closed:
					_ = upstream.Close()
					return
				}
			}
			// Tell the guest to dial its loopback-only native rpc-server.
			if _, err := guest.Write([]byte{1}); err != nil {
				upstream.Close()
				r.finish(guest)
				return
			}
			bridge(upstream, guest)
			r.finish(guest)
		}()
	}
}
func bridge(a, b net.Conn) {
	var once sync.Once
	done := make(chan struct{}, 2)
	pump := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		once.Do(func() { _ = a.Close(); _ = b.Close() })
		done <- struct{}{}
	}
	go pump(a, b)
	go pump(b, a)
	<-done
	<-done
}
func (h *Hub) Endpoints() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []string{}
	for _, r := range h.nodes {
		if r.count() > 0 {
			out = append(out, r.listener.Addr().String())
		}
	}
	sort.Strings(out)
	return out
}
func (h *Hub) ReadyNodes() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]string{}
	for id, r := range h.nodes {
		if r.count() > 0 {
			out[id] = r.listener.Addr().String()
		}
	}
	return out
}
func (h *Hub) Remove(id string) {
	h.mu.Lock()
	r := h.nodes[id]
	delete(h.nodes, id)
	h.mu.Unlock()
	if r != nil {
		close(r.closed)
		r.mu.Lock()
		peers := make([]net.Conn, 0, len(r.peers))
		for c := range r.peers {
			peers = append(peers, c)
		}
		r.mu.Unlock()
		for _, c := range peers {
			r.finish(c)
		}
		_ = r.listener.Close()
	}
}
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	ids := make([]string, 0, len(h.nodes))
	for id := range h.nodes {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, id := range ids {
		h.Remove(id)
	}
}

var ErrTunnelUnavailable = errors.New("RPC tunnel unavailable")

func ValidateRPCAddress(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("RPC native endpoint must bind to loopback: %s", addr)
	}
	return nil
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
// Package rpcmesh bridges unauthenticated ggml RPC over authenticated HTTPS
// tunnels. The native RPC server and the proxy listeners bind to loopback only.
package rpcmesh

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"
)

type relay struct {
	listener  net.Listener
	waiting   chan net.Conn
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
		item = &relay{listener: ln, waiting: make(chan net.Conn, 8), closed: make(chan struct{}), peers: make(map[net.Conn]struct{})}
		h.nodes[nodeID] = item
		go item.accept()
	}
	h.mu.Unlock()
	item.mu.Lock()
	if item.available >= 8 {
		item.mu.Unlock()
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
	// The buffered writer owns HTTP headers; do not pass the socket to ggml until
	// the relay sends a one-byte ready signal when a local RPC connection arrives.
	if _, err = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err == nil {
		err = buf.Flush()
	}
	if err != nil {
		item.finish(conn)
		return
	}
	select {
	case item.waiting <- conn:
	case <-item.closed:
		item.finish(conn)
	case <-r.Context().Done():
		item.finish(conn)
	}
}
func (r *relay) decrement()          { r.mu.Lock(); r.available--; r.mu.Unlock() }
func (r *relay) track(conn net.Conn) { r.mu.Lock(); r.peers[conn] = struct{}{}; r.mu.Unlock() }
func (r *relay) finish(conn net.Conn) {
	r.mu.Lock()
	if _, ok := r.peers[conn]; ok {
		delete(r.peers, conn)
		r.available--
	}
	r.mu.Unlock()
	conn.Close()
}
func (r *relay) count() int { r.mu.Lock(); defer r.mu.Unlock(); return r.available }
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
			select {
			case guest = <-r.waiting:
			case <-timeout.C:
				upstream.Close()
				return
			case <-r.closed:
				upstream.Close()
				return
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
		for {
			select {
			case c := <-r.waiting:
				r.finish(c)
			default:
				return
			}
		}
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

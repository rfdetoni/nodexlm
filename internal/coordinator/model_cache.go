package coordinator

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

type peerCacheItem struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type peerCacheEntry struct {
	Address    string
	CertSHA256 string
	Items      map[string]int64
	UpdatedAt  time.Time
}

type peerCacheAnnounceRequest struct {
	ListenAddr string          `json:"listen_addr"`
	CertSHA256 string          `json:"cert_sha256"`
	PeerPubKey string          `json:"peer_pub_key,omitempty"`
	Items      []peerCacheItem `json:"items"`
}

type peerCacheCandidate struct {
	NodeID     string             `json:"node_id"`
	Name       string             `json:"name"`
	Address    string             `json:"address"`
	CertSHA256 string             `json:"cert_sha256"`
	Size       int64              `json:"size"`
	Grant      protocol.PeerGrant `json:"grant"`
}

func (s *Server) nodeAuthority(w http.ResponseWriter, r *http.Request) {
	if _, id := s.identify(r); id == "" {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	respond(w, http.StatusOK, s.AuthoritySnapshot())
}

func (s *Server) announcePeerCache(w http.ResponseWriter, r *http.Request) {
	_, id := s.identify(r)
	if id == "" {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req peerCacheAnnounceRequest
	if !parse(w, r, &req) {
		return
	}
	if req.PeerPubKey != "" && !protocol.ValidPeerPubKey(req.PeerPubKey) {
		fail(w, http.StatusBadRequest, "invalid peer public key")
		return
	}
	if !protocol.IsSHA256Hex(req.CertSHA256) || len(req.Items) > 128 {
		fail(w, http.StatusBadRequest, "invalid peer cache announcement")
		return
	}
	s.mu.Lock()
	allowLoopback := s.allowLoopbackPeers
	s.mu.Unlock()
	if len(req.Items) > 0 {
		if err := protocol.ValidatePrivatePeerAddress(req.ListenAddr, allowLoopback); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	} else if req.ListenAddr != "" {
		if err := protocol.ValidatePrivatePeerAddress(req.ListenAddr, allowLoopback); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	items := make(map[string]int64, len(req.Items))
	for _, item := range req.Items {
		if !protocol.IsSHA256Hex(item.SHA256) || item.Size < 8 || item.Size > 150<<30 {
			fail(w, http.StatusBadRequest, "invalid cached model digest or size")
			return
		}
		items[item.SHA256] = item.Size
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.nodes[id]
	if n == nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if req.PeerPubKey != "" {
		n.device.PeerPubKey = req.PeerPubKey
	}
	if len(items) == 0 {
		n.cachePeer = nil
	} else {
		n.cachePeer = &peerCacheEntry{
			Address:    req.ListenAddr,
			CertSHA256: req.CertSHA256,
			Items:      items,
			UpdatedAt:  time.Now(),
		}
	}
	respond(w, http.StatusOK, map[string]any{"status": "ok", "count": len(items)})
}

func (s *Server) peerCacheLookup(w http.ResponseWriter, r *http.Request) {
	_, requesterID := s.identify(r)
	if requesterID == "" {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	digest := r.PathValue("sha256")
	if !protocol.IsSHA256Hex(digest) {
		fail(w, http.StatusBadRequest, "invalid sha256 digest")
		return
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	requester := s.nodes[requesterID]
	if requester == nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !protocol.ValidPeerPubKey(requester.device.PeerPubKey) {
		fail(w, http.StatusBadRequest, "requester peer public key not registered")
		return
	}
	var out []peerCacheCandidate
	for peerID, n := range s.nodes {
		if peerID == requesterID || n.cachePeer == nil || n.device.Status == "offline" || n.device.Status == "draining" {
			continue
		}
		if _, revoked := s.revokedNodes[peerID]; revoked {
			continue
		}
		if time.Since(n.device.LastSeen) > heartbeatGrace || time.Since(n.cachePeer.UpdatedAt) > 10*time.Minute {
			continue
		}
		size, ok := n.cachePeer.Items[digest]
		if !ok {
			continue
		}
		grant, err := protocol.SignPeerGrant(s.authorityPriv, protocol.PeerGrant{
			ClusterID:    s.clusterID,
			Epoch:        s.epoch,
			SourceNodeID: peerID,
			TargetPeerID: requesterID,
			TargetPubKey: requester.device.PeerPubKey,
			Scope:        "cache",
			SHA256:       digest,
			Size:         size,
			IssuedAt:     now,
			ExpiresAt:    now.Add(2 * time.Minute),
		})
		if err != nil {
			continue
		}
		out = append(out, peerCacheCandidate{
			NodeID:     peerID,
			Name:       n.device.Name,
			Address:    n.cachePeer.Address,
			CertSHA256: n.cachePeer.CertSHA256,
			Size:       size,
			Grant:      grant,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	respond(w, http.StatusOK, map[string]any{
		"cluster_id": s.clusterID,
		"epoch":      s.epoch,
		"peers":      out,
	})
}

// SetContentCacheDirectory enables model transfer to enrolled nodes only.
// The cache is never exposed through the administrative token or public HTTP.
func (s *Server) SetContentCacheDirectory(dir string) error {
	if os.Getenv("NODEXLM_MODEL_P2P") != "1" {
		return errors.New("authenticated model cache is not enabled")
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	s.mu.Lock()
	s.modelCacheDir = root
	s.mu.Unlock()
	return nil
}

func (s *Server) nodeModelCache(w http.ResponseWriter, r *http.Request) {
	if _, id := s.identify(r); id == "" {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.mu.Lock()
	dir := s.modelCacheDir
	s.mu.Unlock()
	if dir == "" {
		fail(w, http.StatusNotFound, "model cache disabled")
		return
	}
	// Admit up to four authenticated HTTPS streams (the Android range
	// worker ceiling). Clients perform HEAD first to verify/index an immutable
	// SHA-256 once; subsequent GET ranges reuse the verified inode metadata.
	// Reject excess requests instead of queueing unbounded disk reads.
	select {
	case s.modelCacheSlots <- struct{}{}:
		defer func() { <-s.modelCacheSlots }()
	default:
		// Small bounded retry window lets another enrolled Guest finish its
		// current ranges without forcing a public WAN fallback immediately.
		w.Header().Set("Retry-After", "1")
		fail(w, http.StatusTooManyRequests, "model cache busy")
		return
	}
	digest := r.PathValue("sha256")
	file, err := safedownload.OpenVerifiedContent(r.Context(), dir, digest)
	if err != nil {
		fail(w, http.StatusNotFound, "verified model unavailable")
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		fail(w, http.StatusInternalServerError, "model cache unavailable")
		return
	}
	w.Header().Set("ETag", "\""+digest+"\"")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, stat.Name(), time.Time{}, file)
}

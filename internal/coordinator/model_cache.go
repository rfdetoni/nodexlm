package coordinator

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

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

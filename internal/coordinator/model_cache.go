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
	digest := r.PathValue("sha256")
	file, err := safedownload.OpenVerifiedContent(dir, digest)
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

package dashboard

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/rfdetoni/nodexlm/internal/catalog"
)

func (d *Dashboard) importDirectory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Source string `json:"source"`
		Link   bool   `json:"link"`
	}
	if !decode(w, r, &input) {
		return
	}
	if !filepath.IsAbs(input.Source) || len(input.Source) > 4096 {
		failure(w, 400, errors.New("explicit absolute Host directory required"))
		return
	}
	m := d.Manager
	if !m.poolMu.TryLock() {
		failure(w, 409, errors.New("model operation in progress"))
		return
	}
	defer m.poolMu.Unlock()
	m.mu.Lock()
	busy := m.closed || m.loading || m.unloading || m.download != nil && m.download.Status == "downloading"
	m.mu.Unlock()
	if busy {
		failure(w, 409, errors.New("finish loading or downloading before importing"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	imported, err := catalog.ImportDirectory(ctx, input.Source, m.modelDir, input.Link)
	if err != nil {
		reply(w, 409, map[string]any{"imported": len(imported), "error": err.Error()})
		return
	}
	reply(w, 201, map[string]int{"imported": len(imported)})
}

package dashboard

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/rfdetoni/nodexlm/internal/document"
)

var documentSlots = make(chan struct{}, 2)

// Upload raw bytes to a private temporary file. Never trust browser paths or
// archive member names. The shared CLI parser owns format/expansion limits.
func (d *Dashboard) documentText(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		failure(w, 415, errors.New("binary document required"))
		return
	}
	kind := strings.ToLower(r.URL.Query().Get("kind"))
	if kind != "pdf" && kind != "docx" {
		failure(w, 400, errors.New("PDF or DOCX required"))
		return
	}
	select {
	case documentSlots <- struct{}{}:
		defer func() { <-documentSlots }()
	default:
		failure(w, 429, errors.New("document extraction busy"))
		return
	}
	f, err := os.CreateTemp("", "nodexlm-document-*."+kind)
	if err != nil {
		failure(w, 500, errors.New("cannot stage document"))
		return
	}
	defer os.Remove(f.Name())
	n, err := io.Copy(f, io.LimitReader(r.Body, document.MaxUpload+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || n == 0 || n > document.MaxUpload {
		failure(w, 400, errors.New("document unreadable or exceeds 16 MiB"))
		return
	}
	text, err := document.Read(f.Name())
	if err != nil {
		failure(w, 400, errors.New("document extraction failed; verify format, size and local Poppler installation for PDF"))
		return
	}
	reply(w, 200, map[string]string{"text": text})
}

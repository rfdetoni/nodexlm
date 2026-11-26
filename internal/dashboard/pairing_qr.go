package dashboard

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/pairing"
)

// Only the admin UI may render an explicitly supplied temporary code.
// No code is stored or sent to a third-party QR service.
func (d *Dashboard) pairingQR(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var input struct {
		Host string `json:"host"`
		Code string `json:"code"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.Host) > 1024 {
		failure(w, 400, errors.New("pairing endpoint too long"))
		return
	}
	link, err := pairing.Link(input.Host, input.Code)
	if err != nil {
		failure(w, 400, err)
		return
	}
	bin, err := exec.LookPath("qrencode")
	if err != nil {
		failure(w, 503, errors.New("install qrencode on the Host to enable QR pairing"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-t", "PNG", "-o", "-", "-m", "4", "-s", "4")
	cmd.Stdin = strings.NewReader(link)
	png, err := cmd.Output()
	if err != nil || len(png) == 0 || len(png) > 256<<10 {
		failure(w, 500, errors.New("could not render pairing QR"))
		return
	}
	reply(w, 200, map[string]string{"png": base64.StdEncoding.EncodeToString(png)})
}

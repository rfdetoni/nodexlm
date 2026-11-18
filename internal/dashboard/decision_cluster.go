// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/rfdetoni/nodexlm/internal/coordinator"
)

// decide prefers an already connected capable Guest over local inference.
// A worker failure triggers the next worker inside coordinator.Decide, then
// falls back to an already configured local GGUF/Ollaya runtime.
func (d *Dashboard) decide(ctx context.Context, model, path string, body []byte, receive func(int, http.Header, io.Reader) error) error {
	remote, err := d.Cluster.Decide(ctx, model, body)
	if err == nil {
		return receive(http.StatusOK, http.Header{"Content-Type": []string{"application/json"},
			"X-Nodexlm-Decision-Executor": []string{"cluster"}}, bytes.NewReader(remote))
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// Never silently route to an arbitrary endpoint. The local path is the
	// original, guarded manager and uses only known resident models or a
	// configured loopback Ollaya daemon.
	if d.Manager.HasModel(model) ||
		(strings.HasPrefix(model, ollayaPrefix) && os.Getenv("NODEXLM_OLLAYA_URL") != "") {
		return d.Manager.decide(ctx, model, path, body, receive)
	}
	if errors.Is(err, coordinator.ErrNoDecisionWorkers) {
		return d.Manager.decide(ctx, model, path, body, receive)
	}
	return err
}

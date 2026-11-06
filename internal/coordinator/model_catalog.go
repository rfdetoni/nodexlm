// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/rfdetoni/nodexlm/internal/catalog"
	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// modelCatalog lets a securely enrolled phone discover models against the
// Host plus currently RPC-ready guests. It never exposes the Host admin token
// or starts a download using a guest's credentials.
func (s *Server) modelCatalog(w http.ResponseWriter, r *http.Request) {
	n, _ := s.identify(r)
	if n == nil {
		fail(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	hostname, _ := os.Hostname()
	host := hardware.Collect("Host: "+hostname, nil)
	host.Status = "online"
	host.LastSeen = time.Now()
	devices := []protocol.Device{host}
	rpcReady := s.RPCNodes()
	for _, device := range s.DevicesSnapshot() {
		if _, ok := rpcReady[device.ID]; ok && device.Runtime.RPCReady {
			devices = append(devices, device)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	models, err := (catalog.Hub{}).Search(ctx, r.URL.Query().Get("q"))
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	summary := clusterplan.Aggregate(devices, time.Now())
	results := make([]clusterplan.Recommendation, 0, len(models))
	for _, model := range models {
		results = append(results, clusterplan.Assess(summary, model))
	}
	respond(w, http.StatusOK, results)
}

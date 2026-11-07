// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/rfdetoni/nodexlm/internal/catalog"
	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/coordinator"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/requestlog"
)

//go:embed static/*
var static embed.FS

type Dashboard struct {
	Manager    *Manager
	Cluster    *coordinator.Server
	admin      [32]byte
	apiKey     [32]byte
	apiEnabled bool
}

func New(manager *Manager, cluster *coordinator.Server, adminToken string) *Dashboard {
	return &Dashboard{Manager: manager, Cluster: cluster, admin: sha256.Sum256([]byte(adminToken))}
}
func (d *Dashboard) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", d.api(func(w http.ResponseWriter, r *http.Request) { d.Cluster.WriteMetrics(w) }))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		b, err := static.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "UI unavailable", 500)
			return
		}
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		b, _ := static.ReadFile("static/app.js")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /styles.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		b, _ := static.ReadFile("static/styles.css")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /api/plan", d.api(func(w http.ResponseWriter, r *http.Request) {
		plan, err := d.Manager.Plan()
		if err != nil {
			failure(w, 400, err)
			return
		}
		reply(w, 200, plan)
	}))
	mux.HandleFunc("GET /api/status", d.api(func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"runtime": d.Manager.State(), "settings": d.Manager.Settings(), "download": d.Manager.DownloadState()})
	}))
	mux.HandleFunc("GET /api/cluster", d.api(func(w http.ResponseWriter, r *http.Request) {
		name, _ := os.Hostname()
		host := hardware.Collect("Host: "+name, nil)
		host.Status = "online"
		host.LastSeen = time.Now()
		nodes := d.Cluster.DevicesSnapshot()
		devices := append([]protocol.Device{host}, nodes...)
		summary := clusterplan.Aggregate(devices, time.Now())
		reply(w, 200, map[string]any{"resources": summary, "rpc_guests": d.Cluster.RPCNodes()})
	}))
	mux.HandleFunc("GET /api/catalog", d.api(func(w http.ResponseWriter, r *http.Request) {
		name, _ := os.Hostname()
		host := hardware.Collect("Host: "+name, nil)
		host.Status = "online"
		host.LastSeen = time.Now()
		summary := clusterplan.Aggregate(append([]protocol.Device{host}, d.Cluster.DevicesSnapshot()...), time.Now())
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		models, err := (catalog.Hub{}).Search(ctx, r.URL.Query().Get("q"))
		if err != nil {
			failure(w, 400, err)
			return
		}
		recommendations := make([]clusterplan.Recommendation, 0, len(models))
		for _, m := range models {
			recommendations = append(recommendations, clusterplan.Assess(summary, m))
		}
		reply(w, 200, recommendations)
	}))
	mux.HandleFunc("GET /api/models", d.api(func(w http.ResponseWriter, r *http.Request) {
		items, err := d.Manager.Models()
		if err != nil {
			failure(w, 500, err)
			return
		}
		reply(w, 200, items)
	}))
	mux.HandleFunc("DELETE /api/download", d.api(func(w http.ResponseWriter, r *http.Request) {
		d.Manager.CancelDownload()
		reply(w, 202, map[string]string{"status": "cancelling"})
	}))
	mux.HandleFunc("GET /api/download", d.api(func(w http.ResponseWriter, r *http.Request) { reply(w, 200, d.Manager.DownloadState()) }))
	mux.HandleFunc("POST /api/download", d.api(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL  string `json:"url"`
			Name string `json:"name"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := d.Manager.StartDownload(in.URL, in.Name); err != nil {
			failure(w, 400, err)
			return
		}
		reply(w, 202, map[string]string{"status": "downloading"})
	}))
	mux.HandleFunc("PUT /api/settings", d.api(func(w http.ResponseWriter, r *http.Request) {
		var s Settings
		if !decode(w, r, &s) {
			return
		}
		if err := d.Manager.Update(s); err != nil {
			failure(w, 400, err)
			return
		}
		reply(w, 200, s)
	}))
	mux.HandleFunc("POST /api/load", d.api(func(w http.ResponseWriter, r *http.Request) {
		if err := d.Manager.StartLoad(); err != nil {
			failure(w, 400, err)
			return
		}
		reply(w, 202, d.Manager.State())
	}))
	mux.HandleFunc("POST /api/unload", d.api(func(w http.ResponseWriter, r *http.Request) { d.Manager.Unload(); reply(w, 200, d.Manager.State()) }))

	mux.HandleFunc("POST /api/chat", d.api(d.chat))
	mux.HandleFunc("POST /v1/chat/completions", d.inference(d.chat))
	mux.HandleFunc("GET /v1/models", d.inference(func(w http.ResponseWriter, r *http.Request) {
		models := []any{}
		if name, ok := d.Manager.State()["model"].(string); ok && name != "" {
			models = append(models, map[string]any{"id": name, "object": "model", "created": 0, "owned_by": "nodexlm"})
		}
		reply(w, 200, map[string]any{"object": "list", "data": models})
	}))

	return requestlog.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Dashboard access is exclusively via loopback; use an SSH tunnel for remote management.
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if !(strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
			http.Error(w, "localhost only", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme != "http" || u.Host != r.Host {
				http.Error(w, "origin rejected", 403)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if strings.HasPrefix(r.URL.Path, "/v1/") && r.URL.Path != "/v1/models" && r.URL.Path != "/v1/chat/completions" {
			d.Cluster.Handler().ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}))
}
func (d *Dashboard) api(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "unauthorized", 401)
			return
		}
		digest := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
		if subtle.ConstantTimeCompare(digest[:], d.admin[:]) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		next(w, r)
	}
}
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON required", 415)
		return false
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		http.Error(w, "invalid JSON", 400)
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		http.Error(w, "unexpected JSON trailing data", 400)
		return false
	}
	return true
}
func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, code int, err error) {
	reply(w, code, map[string]string{"error": err.Error()})
}

// SetAPIKey configures a credential restricted to inference endpoints. Call before serving.
func (d *Dashboard) SetAPIKey(key string) error {
	if len(key) < 32 {
		return fmt.Errorf("API key must contain at least 32 characters")
	}
	digest := sha256.Sum256([]byte(key))
	if subtle.ConstantTimeCompare(digest[:], d.admin[:]) == 1 {
		return fmt.Errorf("API key must differ from admin token")
	}
	d.apiKey = digest
	d.apiEnabled = true
	return nil
}
func (d *Dashboard) inference(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		hash := sha256.Sum256([]byte(value))
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || (subtle.ConstantTimeCompare(hash[:], d.admin[:]) != 1 && !(d.apiEnabled && subtle.ConstantTimeCompare(hash[:], d.apiKey[:]) == 1)) {
			http.Error(w, "unauthorized", 401)
			return
		}
		next(w, r)
	}
}

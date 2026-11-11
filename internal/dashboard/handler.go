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
	compat "github.com/rfdetoni/nodexlm/internal/openai"
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
	openAI := &compat.Handler{Forward: d.Manager.openAI, Target: d.chat, Models: d.Manager.LoadedModels, Observe: d.Manager.recordInference}
	inferenceAPI := d.inference(openAI.ServeHTTP)
	mux.HandleFunc("GET /metrics", d.api(func(w http.ResponseWriter, r *http.Request) { d.Cluster.WriteMetrics(w); d.Manager.WriteMetrics(w) }))
	mux.HandleFunc("POST /api/bench/inference", d.api(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model string `json:"model"`
			Node  string `json:"node_id"`
		}
		if !decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		sample, err := d.Manager.BenchmarkInference(ctx, input.Model, input.Node)
		if err != nil {
			failure(w, 409, err)
			return
		}
		reply(w, 200, sample)
	}))
	mux.HandleFunc("GET /api/observability", d.api(func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"inference": d.Manager.InferenceSamples(), "nodes": d.Cluster.Observations()})
	}))
	mux.HandleFunc("GET /api/resident-models", d.api(func(w http.ResponseWriter, r *http.Request) { reply(w, 200, d.Manager.LoadedModels()) }))
	mux.HandleFunc("POST /api/resident-models", d.api(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model string `json:"model"`
		}
		if !decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
		defer cancel()
		if err := d.Manager.LoadModel(ctx, input.Model); err != nil {
			failure(w, 409, err)
			return
		}
		reply(w, 201, d.Manager.LoadedModels())
	}))
	mux.HandleFunc("DELETE /api/resident-models/{model}", d.api(func(w http.ResponseWriter, r *http.Request) {
		d.Manager.UnloadModel(r.PathValue("model"))
		reply(w, 200, d.Manager.LoadedModels())
	}))
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
		reply(w, 200, map[string]any{"resources": summary, "rpc_guests": d.Cluster.RPCNodes(), "devices": devices})
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
	mux.HandleFunc("GET /api/models/folder", d.api(func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]string{"path": d.Manager.ModelFolder()})
	}))
	mux.HandleFunc("POST /api/models/folder/open", d.api(func(w http.ResponseWriter, r *http.Request) {
		if err := d.Manager.OpenModelFolder(); err != nil {
			failure(w, 500, err)
			return
		}
		reply(w, 200, map[string]string{"status": "opened"})
	}))
	mux.HandleFunc("DELETE /api/models/{name}", d.api(func(w http.ResponseWriter, r *http.Request) {
		if err := d.Manager.DeleteModel(r.PathValue("name")); err != nil {
			failure(w, 409, err)
			return
		}
		reply(w, 200, map[string]string{"status": "deleted"})
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

	return requestlog.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Dashboard access is exclusively via loopback; use an SSH tunnel for remote management.
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(host)
		if !(strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
			if compat.IsPath(r.URL.Path) {
				compat.Error(w, 403, "API access requires loopback", "", "forbidden")
			} else {
				http.Error(w, "localhost only", 403)
			}
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme != "http" || u.Host != r.Host {
				if compat.IsPath(r.URL.Path) {
					compat.Error(w, 403, "Origin rejected", "", "forbidden")
				} else {
					http.Error(w, "origin rejected", 403)
				}
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		if compat.IsPath(r.URL.Path) {
			inferenceAPI(w, r)
			return
		}
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
			if compat.IsPath(r.URL.Path) {
				compat.Error(w, 401, "Invalid API key", "", "invalid_api_key")
			} else {
				http.Error(w, "unauthorized", 401)
			}
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
			if compat.IsPath(r.URL.Path) {
				compat.Error(w, 401, "Invalid API key", "", "invalid_api_key")
			} else {
				http.Error(w, "unauthorized", 401)
			}
			return
		}
		next(w, r)
	}
}

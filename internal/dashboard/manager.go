// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// Settings are persisted independently of the UI. The model weights live only
// in the llama-server child process and can be unloaded without stopping NodexLM.
type Settings struct {
	Model         string `json:"model"`
	Threads       int    `json:"threads"`
	ContextTokens int    `json:"context_tokens"`
	GPULayers     string `json:"gpu_layers"`
	MoECPU        bool   `json:"moe_cpu"`
	MoECPULayers  int    `json:"moe_cpu_layers"`
	MoECacheMiB   int    `json:"moe_cache_mib"`
	Distributed   bool   `json:"distributed"`
}

type Download struct {
	Name     string `json:"name"`
	Received int64  `json:"received"`
	Total    int64  `json:"total"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type Manager struct {
	mu                    sync.Mutex
	chatMu                sync.Mutex
	dir, modelDir, binary string
	settings              Settings
	engine                *local.Managed
	current               string
	download              *Download
	cancel                context.CancelFunc
	httpClient            *http.Client
	rpcEndpoints          func() []string
}

func NewManager(dir, modelDir, binary string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(modelDir, 0700); err != nil {
		return nil, err
	}
	m := &Manager{dir: dir, modelDir: modelDir, binary: binary, settings: Settings{Threads: max(1, runtime.NumCPU()-1), ContextTokens: 2048, GPULayers: "auto"}}
	b, err := os.ReadFile(filepath.Join(dir, "runtime.json"))
	if err == nil {
		if err = json.Unmarshal(b, &m.settings); err != nil {
			return nil, fmt.Errorf("runtime settings: %w", err)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err = m.validate(m.settings); err != nil {
		return nil, fmt.Errorf("runtime settings: %w", err)
	}
	m.httpClient = &http.Client{Timeout: 0, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 8 {
			return errors.New("too many redirects")
		}
		return validateURL(req.URL)
	}, Transport: &http.Transport{Proxy: nil, DialContext: safeDial, MaxIdleConns: 2, MaxIdleConnsPerHost: 1, IdleConnTimeout: 10 * time.Second}}
	return m, nil
}

func (m *Manager) SetRPCProvider(provider func() []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rpcEndpoints = provider
}
func (m *Manager) Models() ([]protocol.ModelRecord, error) { return local.Models(m.modelDir) }
func (m *Manager) Settings() Settings                      { m.mu.Lock(); defer m.mu.Unlock(); return m.settings }
func (m *Manager) State() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"model": m.current, "loaded": m.engine != nil}
}
func (m *Manager) DownloadState() *Download {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.download == nil {
		return nil
	}
	s := *m.download
	return &s
}
func (m *Manager) validate(s Settings) error {
	if s.Threads < 1 || s.Threads > 256 || s.ContextTokens < 256 || s.ContextTokens > 262144 || s.MoECPULayers < 0 || s.MoECPULayers > 1024 || s.MoECacheMiB < 0 || s.MoECacheMiB > 65536 || s.MoECPU && s.MoECPULayers > 0 {
		return errors.New("invalid runtime limits or conflicting MoE settings")
	}
	if s.GPULayers != "auto" && s.GPULayers != "all" {
		var n int
		if _, err := fmt.Sscanf(s.GPULayers, "%d", &n); err != nil || n < 0 || n > 1024 || fmt.Sprint(n) != s.GPULayers {
			return errors.New("invalid gpu_layers")
		}
	}
	if s.Model != "" {
		if _, err := modelPath(m.modelDir, s.Model); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) Update(s Settings) error {
	if err := m.validate(s); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		return errors.New("unload model before changing runtime settings")
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := writePrivate(filepath.Join(m.dir, "runtime.json"), raw); err != nil {
		return err
	}
	m.settings = s
	return nil
}
func writePrivate(path string, body []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".runtime-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func modelPath(dir, name string) (string, error) {
	if name == "" || filepath.Base(name) != name || !strings.EqualFold(filepath.Ext(name), ".gguf") || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
		return "", errors.New("model name must be a GGUF file in the models directory")
	}
	path := filepath.Join(dir, name)
	// Do not follow symlinks out of the managed model directory.
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return "", errors.New("model must be a regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}
func (m *Manager) Load(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		return errors.New("model already loaded")
	}
	s := m.settings
	if s.Model == "" {
		return errors.New("select a model first")
	}
	path, err := modelPath(m.modelDir, s.Model)
	if err != nil {
		return err
	}
	if _, err = os.Stat(path); err != nil {
		return err
	}
	rpcEndpoints := []string(nil)
	if s.Distributed {
		if m.rpcEndpoints == nil {
			return errors.New("distributed inference needs a Host coordinator")
		}
		rpcEndpoints = m.rpcEndpoints()
		if len(rpcEndpoints) == 0 {
			return errors.New("no authenticated RPC Guests are connected")
		}
	}
	var resolved string
	if s.Distributed && (m.binary == "" || m.binary == "llama-server") {
		resolved, _, err = llamaruntime.EnsureDistributed(ctx, m.dir, "auto")
	} else {
		resolved, err = llamaruntime.AutoExecutable(ctx, m.dir, m.binary)
	}
	if err != nil {
		return err
	}
	if s.Distributed && !llamaruntime.SupportsRPC(ctx, resolved) {
		return errors.New("llama-server was built without GGML_RPC; use the bundled RPC runtime")
	}
	engine := &local.Managed{Binary: resolved, ModelPath: path, Alias: s.Model, Threads: s.Threads, ContextTokens: s.ContextTokens, GPULayers: s.GPULayers, MoECPU: s.MoECPU, MoECPULayers: s.MoECPULayers, MoECacheMiB: s.MoECacheMiB, KeepLoaded: true}
	if s.Distributed {
		engine.RPCServers = rpcEndpoints
		if s.GPULayers == "auto" {
			engine.GPULayers = "all"
		}
	}
	if err = engine.Load(ctx); err != nil {
		engine.Close()
		return err
	}
	m.engine = engine
	m.current = s.Model
	return nil
}
func (m *Manager) Unload() {
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engine != nil {
		m.engine.Close()
		m.engine = nil
		m.current = ""
	}
}
func (m *Manager) Generate(ctx context.Context, req protocol.ChatRequest) (string, error) {
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	m.mu.Lock()
	engine := m.engine
	model := m.current
	m.mu.Unlock()
	if engine == nil {
		return "", errors.New("no model loaded")
	}
	req.Model = model
	return engine.Generate(ctx, req)
}
func (m *Manager) Close() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.Unload()
}
func (m *Manager) StartDownload(urlText, name string) error {
	if name == "" {
		return errors.New("filename required")
	}
	path, err := modelPath(m.modelDir, name)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(path); err == nil {
		return errors.New("model file already exists")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	u, err := parseModelURL(urlText)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.download != nil && m.download.Status == "downloading" {
		return errors.New("another download is in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.download = &Download{Name: name, Status: "downloading"}
	go m.downloadModel(ctx, u.String(), path, name)
	return nil
}
func (m *Manager) downloadModel(ctx context.Context, source, path, name string) {
	status := "failed"
	errorText := ""
	defer func() {
		m.mu.Lock()
		m.download.Status = status
		m.download.Error = errorText
		m.cancel = nil
		m.mu.Unlock()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		errorText = err.Error()
		return
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		errorText = err.Error()
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		errorText = fmt.Sprintf("download HTTP %d", resp.StatusCode)
		return
	}
	const maxSize int64 = 150 << 30
	if resp.ContentLength > maxSize {
		errorText = "model exceeds 150 GiB limit"
		return
	}
	m.mu.Lock()
	m.download.Total = resp.ContentLength
	m.mu.Unlock()
	f, err := os.CreateTemp(m.modelDir, ".nodexlm-download-")
	if err != nil {
		errorText = err.Error()
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		errorText = err.Error()
		return
	}
	counter := &countWriter{w: f, update: func(n int64) { m.mu.Lock(); m.download.Received = n; m.mu.Unlock() }}
	_, err = io.Copy(counter, io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		errorText = err.Error()
		return
	}
	if counter.n > maxSize {
		errorText = "model exceeds 150 GiB limit"
		return
	}
	if resp.ContentLength >= 0 && counter.n != resp.ContentLength {
		errorText = "incomplete model download"
		return
	}
	if err = f.Sync(); err != nil {
		errorText = err.Error()
		return
	}
	if err = f.Close(); err != nil {
		errorText = err.Error()
		return
	}
	if _, err = local.InspectGGUF(f.Name()); err != nil {
		errorText = "invalid GGUF: " + err.Error()
		return
	}
	// Hard-link prevents overwriting an existing model after a concurrent download.
	if err = os.Link(f.Name(), path); err != nil {
		errorText = err.Error()
		return
	}
	m.mu.Lock()
	m.download.Received = counter.n
	m.mu.Unlock()
	status = "completed"
}

type countWriter struct {
	w      io.Writer
	n      int64
	update func(int64)
	last   time.Time
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if time.Since(c.last) >= 250*time.Millisecond {
		c.update(c.n)
		c.last = time.Now()
	}
	return n, err
}

// safeDial blocks IP ranges useful for SSRF even after DNS resolution. It does
// not accept proxies; the download destination must be a public HTTPS host.
func safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dial := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, item := range ips {
		ip := item.IP
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		c, err := dial.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
	}
	return nil, errors.New("public download destination unavailable")
}

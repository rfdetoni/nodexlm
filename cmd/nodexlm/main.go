// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/config"
	"github.com/rfdetoni/nodexlm/internal/coordinator"
	"github.com/rfdetoni/nodexlm/internal/dashboard"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/netclient"
	"github.com/rfdetoni/nodexlm/internal/pairing"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
	"github.com/rfdetoni/nodexlm/internal/worker"
)

var version = "0.1.0-alpha"

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("nodexlm", "error", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "version":
		fmt.Println("nodexlm", version)
		return nil
	case "cert":
		return cert(args[1:])
	case "web-token":
		return webToken(args[1:])
	case "coordinator":
		return serve(args[1:])
	case "host":
		return serve(append([]string{"--pair", "--addr", "0.0.0.0:8443"}, args[1:]...))
	case "join":
		return join(args[1:])
	case "pairing":
		return newPairing(args[1:])
	case "node":
		return node(args[1:])
	case "rpc-node":
		return rpcNode(args[1:])
	case "nodes":
		return nodes(args[1:])
	case "revoke":
		return revoke(args[1:])
	case "chat":
		return chat(args[1:])
	case "local":
		return localChat(args[1:])
	case "models":
		return models(args[1:])
	case "hub":
		return hub(args[1:])
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

const usage = `NodexLM — privacy-first distributed local inference

  nodexlm cert --hosts 127.0.0.1,localhost,192.168.1.10
  nodexlm host                       # HTTPS host + LAN discovery + Web UI on localhost:8787
  nodexlm host --web-addr off        # headless without the Web UI
  nodexlm web-token                  # print local Web UI admin credential
  nodexlm coordinator --addr 0.0.0.0:8443 --pair
  nodexlm join                       # prompts for 8-character code
  nodexlm join --code ABCD2345 --name worker-1
  nodexlm pairing # issue a fresh 8-character code
  nodexlm join --server https://host:8443 --name worker-1 --model model.gguf --ca ./server.crt
  nodexlm node --backend http://127.0.0.1:8080 --model model.gguf --ca ./server.crt
  nodexlm rpc-node                   # shares CPU/GPU for ONE distributed GGUF
  nodexlm nodes --server https://host:8443 --ca ./server.crt
  nodexlm revoke --id NODE_ID --server https://host:8443 --ca ./server.crt
  nodexlm chat --server https://host:8443 --model model.gguf --prompt "Hello" --ca ./server.crt
  nodexlm models --dir ./models
  nodexlm hub search --query "qwen gguf" --json [--server https://host:8443 --ca ./server.crt]
  nodexlm hub download --repo owner/repo --file model.Q4_K_M.gguf --dir ./models
  nodexlm hub import --file /path/to/manual.gguf --dir ./models
  nodexlm local --model ./models/model.gguf --prompt "hello"
  nodexlm local --model ./models/moe.gguf --moe-cpu --prompt "hello"
  nodexlm node --model moe.gguf --model-path ./models/moe.gguf --moe-cpu --ca ./server.crt

The --pair mode generates TLS identity and secure local secrets automatically.
Without --pair, legacy secrets are read from NODEXLM_ADMIN_TOKEN and NODEXLM_JOIN_TOKEN.
Never pass secrets as flags. Use a trusted LAN/VPN and HTTPS.
`

func fs(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	return f
}
func cert(args []string) error {
	f := fs("cert")
	hosts := f.String("hosts", "127.0.0.1,localhost", "certificate hostnames/IPs")
	dir := f.String("dir", config.DefaultDir(), "configuration directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	return config.CreateCertificate(filepath.Join(*dir, "server.crt"), filepath.Join(*dir, "server.key"), strings.Split(*hosts, ","))
}
func ctx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
func webToken(args []string) error {
	f := fs("web-token")
	dir := f.String("dir", config.DefaultDir(), "configuration directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	secret := config.AdminSecret(*dir)
	if secret == "" {
		return errors.New("no local admin token; start nodexlm host first")
	}
	fmt.Println(secret)
	return nil
}

func serve(args []string) error {
	f := fs("coordinator")
	addr := f.String("addr", "127.0.0.1:8443", "HTTPS listen address; use 0.0.0.0:8443 for LAN")
	dir := f.String("dir", config.DefaultDir(), "configuration directory")
	enablePair := f.Bool("pair", false, "enable 8-character local pairing code (initial window: 10 minutes)")
	discoveryPort := f.Int("discovery-port", pairing.DiscoveryPort, "UDP discovery port")
	webAddr := f.String("web-addr", "127.0.0.1:8787", "loopback HTTP web management listener; off to disable")
	modelDir := f.String("model-dir", "", "directory containing managed GGUF models")
	llamaBin := f.String("llama-bin", "llama-server", "llama-server executable used by Web UI")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *discoveryPort < 1 || *discoveryPort > 65535 {
		return errors.New("invalid discovery port")
	}
	adminToken := os.Getenv("NODEXLM_ADMIN_TOKEN")
	joinToken := os.Getenv("NODEXLM_JOIN_TOKEN")
	if *enablePair && (adminToken == "" || joinToken == "") {
		saved, err := config.EnsureSecrets(*dir)
		if err != nil {
			return err
		}
		if adminToken == "" {
			adminToken = saved.Admin
		}
		if joinToken == "" {
			joinToken = saved.Join
		}
	}
	s, err := coordinator.New(adminToken, joinToken)
	if err != nil {
		return err
	}
	defer s.CloseRPC()
	if err := s.SetStore(filepath.Join(*dir, "registry.json")); err != nil {
		return err
	}
	certFile := filepath.Join(*dir, "server.crt")
	keyFile := filepath.Join(*dir, "server.key")
	if *enablePair {
		if _, err := os.Stat(certFile); errors.Is(err, os.ErrNotExist) {
			hosts := []string{"localhost", "127.0.0.1"}
			for _, ep := range pairing.LocalEndpoints(*addr) {
				u, err := url.Parse(ep)
				if err == nil {
					hosts = append(hosts, u.Hostname())
				}
			}
			if err := config.CreateCertificate(certFile, keyFile, hosts); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	pem, err := os.ReadFile(certFile)
	if err != nil {
		return fmt.Errorf("coordinator TLS certificate: %w", err)
	}
	if err := s.PreparePairing(pem); err != nil {
		return err
	}
	if *enablePair {
		code, err := s.StartPairing(10 * time.Minute)
		if err != nil {
			return err
		}
		fmt.Printf("\nNODEXLM PAIRING CODE: %s\nAvailable for 10 minutes on the same local network.\n\n", code)
	}
	srv := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	var webServer *http.Server
	var manager *dashboard.Manager
	var webListener net.Listener
	if *webAddr != "off" {
		host, _, err := net.SplitHostPort(*webAddr)
		if err != nil {
			return fmt.Errorf("invalid web listener: %w", err)
		}
		parsed := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (parsed == nil || !parsed.IsLoopback()) {
			return errors.New("web UI must bind to localhost; use an SSH tunnel for remote management")
		}
		if *modelDir == "" {
			*modelDir = filepath.Join(*dir, "models")
		}
		manager, err = dashboard.NewManager(*dir, *modelDir, *llamaBin)
		if err != nil {
			return err
		}
		defer manager.Close()
		manager.SetRPCProvider(func() []dashboard.RPCGuest {
			ready := s.RPCNodes()
			profiles := s.DevicesSnapshot()
			out := make([]dashboard.RPCGuest, 0, len(ready))
			for _, profile := range profiles {
				endpoint, ok := ready[profile.ID]
				if !ok || profile.Status != "online" || time.Since(profile.LastSeen) > 65*time.Second {
					continue
				}
				out = append(out, dashboard.RPCGuest{Endpoint: endpoint, CapacityBytes: clusterplan.Budget(profile)})
			}
			return out
		})
		webListener, err = net.Listen("tcp", *webAddr)
		if err != nil {
			return fmt.Errorf("Web UI listen: %w", err)
		}
		defer webListener.Close()
		webServer = &http.Server{Handler: dashboard.New(manager, s, adminToken).Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
		go func() {
			if err := webServer.Serve(webListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("Web UI failed", "error", err)
			}
		}()
		slog.Info("Web management ready", "url", "http://"+webListener.Addr().String(), "auth", "run nodexlm web-token on host")
	}
	c, cancel := ctx()
	defer cancel()
	go s.Cleanup(c)
	endpoints := pairing.LocalEndpoints(*addr)
	go func() {
		if err := pairing.ListenDiscovery(c, *discoveryPort, func() []pairing.Offer { return s.Offers(endpoints) }); err != nil && c.Err() == nil {
			slog.Error("LAN discovery unavailable", "error", err)
		}
	}()
	go func() {
		<-c.Done()
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if webServer != nil {
			_ = webServer.Shutdown(shutdownCtx)
		}
		_ = srv.Shutdown(shutdownCtx)
	}()
	slog.Info("HTTPS coordinator listening", "addr", *addr, "discoveryUDP", *discoveryPort)
	e := srv.ListenAndServeTLS(certFile, keyFile)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}

func makeClient(server, secret, ca, nodeID string) (*netclient.Client, error) {
	c, err := netclient.New(server, secret, nodeID)
	if err != nil {
		return nil, err
	}
	if ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid CA certificate")
		}
		if err := c.UseRoots(pool); err != nil {
			return nil, err
		}
	}
	return c, nil
}
func join(args []string) error {
	f := fs("join")
	server := f.String("server", "", "HTTPS coordinator; optional when using --code on LAN")
	code := f.String("code", "", "8-character pairing code displayed by the host")
	discoveryPort := f.Int("discovery-port", pairing.DiscoveryPort, "UDP discovery port")
	ca := f.String("ca", "", "legacy trusted coordinator certificate")
	name := f.String("name", "", "node display name")
	model := f.String("model", "", "optional model name advertised by node")
	dir := f.String("dir", config.DefaultDir(), "configuration directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(*dir, "agent.json")); err == nil {
		return errors.New("node already enrolled; refusing to overwrite credentials")
	}
	nodeName := *name
	if nodeName == "" {
		nodeName, _ = os.Hostname()
	}
	d := protocol.Device{Name: nodeName, Platform: runtime.GOOS, Architecture: runtime.GOARCH, CPUCount: runtime.NumCPU()}
	if *model != "" {
		d.Models = []string{*model}
	}
	c, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	if *code == "-" || (*code == "" && (*server == "" || os.Getenv("NODEXLM_JOIN_TOKEN") == "" || *model == "")) {
		if *code == "" {
			fmt.Fprint(os.Stderr, "Enter host pairing code: ")
		}
		line, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
		if readErr != nil && line == "" {
			return fmt.Errorf("could not read pairing code: %w", readErr)
		}
		*code = strings.TrimSpace(line)
	}
	var cli *netclient.Client
	var certPin string
	var err error
	if *code != "" {
		validated := pairing.Normalize(*code)
		if !pairing.ValidCode(validated) {
			return errors.New("--code must contain 8 alphanumeric characters")
		}
		var candidateEndpoints []string
		if *server != "" {
			candidateEndpoints = []string{strings.TrimRight(*server, "/")}
		} else {
			offers, discoverErr := pairing.Discover(c, *discoveryPort, 3*time.Second)
			if discoverErr != nil {
				return fmt.Errorf("LAN discovery failed: %w", discoverErr)
			}
			for _, offer := range offers {
				candidateEndpoints = append(candidateEndpoints, offer.Endpoint)
			}
		}
		if len(candidateEndpoints) == 0 {
			return errors.New("no coordinator discovered; check LAN/Wi-Fi isolation and UDP firewall, or provide --server")
		}
		for _, endpoint := range candidateEndpoints {
			u, parseErr := url.Parse(endpoint)
			if parseErr != nil || u.Scheme != "https" || !pairing.SameLAN(net.ParseIP(u.Hostname())) {
				continue
			}
			offer, der, fetchErr := pairing.FetchUntrustedOffer(c, endpoint)
			if fetchErr != nil {
				continue
			}
			if verifyErr := pairing.VerifyOffer(validated, offer, der); verifyErr != nil {
				continue
			}
			pinSum := sha256.Sum256(der)
			if cli != nil {
				if certPin == hex.EncodeToString(pinSum[:]) {
					continue
				} // multiple interfaces of one coordinator
				return errors.New("multiple coordinators match this code; specify --server explicitly")
			}
			certPin = hex.EncodeToString(pinSum[:])
			cli, err = netclient.New(endpoint, "", "")
			if err != nil {
				return err
			}
			if err = cli.UsePinnedCertificate(certPin); err != nil {
				return err
			}
		}
		if cli == nil {
			return errors.New("no coordinator matching this pairing code and TLS certificate found")
		}
		var cred protocol.Credential
		_, err = cli.Request(c, http.MethodPost, "/v1/pairing/enroll", map[string]any{"code": validated, "device": d}, &cred)
		if err != nil {
			return err
		}
		if err := config.SaveAgent(*dir, config.Agent{Coordinator: cli.Base, Node: cred, CertificateFingerprint: certPin}); err != nil {
			return err
		}
		fmt.Println("Node paired securely:", nodeName, "ID", cred.NodeID)
		return nil
	}
	if *server == "" || *model == "" {
		return errors.New("legacy join requires --server and --model, or use --code")
	}
	secret := os.Getenv("NODEXLM_JOIN_TOKEN")
	if secret == "" {
		return errors.New("NODEXLM_JOIN_TOKEN required; use --code for simple LAN pairing")
	}
	cli, err = makeClient(*server, secret, *ca, "")
	if err != nil {
		return err
	}
	var cred protocol.Credential
	_, err = cli.Request(c, http.MethodPost, "/v1/nodes/enroll", protocol.Enrollment{Device: d}, &cred)
	if err != nil {
		return err
	}
	if err := config.SaveAgent(*dir, config.Agent{Coordinator: *server, Node: cred}); err != nil {
		return err
	}
	fmt.Println("Enrolled node", nodeName, "with ID", cred.NodeID)
	return nil
}

func newPairing(args []string) error {
	f := fs("pairing")
	server := f.String("server", "https://127.0.0.1:8443", "host coordinator")
	ca := f.String("ca", "", "trusted certificate (optional)")
	dir := f.String("dir", config.DefaultDir(), "local coordinator configuration directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	secret := config.AdminSecret(*dir)
	if secret == "" {
		return errors.New("no coordinator admin credential; configure NODEXLM_ADMIN_TOKEN or use coordinator --pair")
	}
	cli, err := makeClient(*server, secret, *ca, "")
	if err != nil {
		return err
	}
	if *ca == "" {
		pem, err := os.ReadFile(filepath.Join(*dir, "server.crt"))
		if err != nil {
			return errors.New("--ca is required when renewing a remote coordinator")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return errors.New("invalid server certificate")
		}
		if err := cli.UseRoots(pool); err != nil {
			return err
		}
	}
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var result struct {
		Code    string `json:"code"`
		Expires string `json:"expires_in"`
	}
	if _, err := cli.Request(c, http.MethodPost, "/v1/pairing/start", map[string]string{}, &result); err != nil {
		return err
	}
	fmt.Printf("NODEXLM PAIRING CODE: %s (valid %s)\n", result.Code, result.Expires)
	return nil
}

func node(args []string) error {
	f := fs("node")
	backend := f.String("backend", "", "existing localhost llama-server (optional)")
	path := f.String("model-path", "", "GGUF file for lazy auto-managed llama-server")
	bin := f.String("llama-bin", "llama-server", "llama-server executable")
	idle := f.Duration("idle", 2*time.Minute, "unload model after idle; 0 for per-job unload")
	contextTokens := f.Int("ctx", 2048, "context window tokens")
	threads := f.Int("threads", 0, "CPU threads, 0 for auto")
	gpu := f.String("gpu-layers", "auto", "GPU layers: auto, all, or count")
	moeCPU := f.Bool("moe-cpu", false, "keep all MoE experts in CPU RAM")
	moeCPULayers := f.Int("moe-cpu-layers", 0, "keep MoE experts of first N layers in CPU RAM")
	moeCacheMiB := f.Int("moe-cache-mib", 0, "GPU cache MiB for CPU-resident MoE experts")
	model := f.String("model", "", "model name advertised by local llama-server")
	ca := f.String("ca", "", "trusted coordinator certificate")
	dir := f.String("dir", config.DefaultDir(), "configuration directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	agent, err := config.ReadAgent(*dir)
	if err != nil {
		return fmt.Errorf("node not enrolled: %w", err)
	}
	cli, err := makeClient(agent.Coordinator, agent.Node.Token, *ca, agent.Node.NodeID)
	if err != nil {
		return err
	}
	if *ca == "" && agent.CertificateFingerprint != "" {
		if err := cli.UsePinnedCertificate(agent.CertificateFingerprint); err != nil {
			return err
		}
	}
	if *model == "" {
		return errors.New("--model required")
	}
	var engine worker.Generator
	var managed *local.Managed
	if *backend != "" {
		engine, err = local.New(*backend)
		if err != nil {
			return err
		}
	} else {
		if *path == "" {
			return errors.New("provide --model-path for lazy loading or --backend for an already running server")
		}
		cpu := *threads
		if cpu <= 0 {
			cpu = runtime.NumCPU() - 1
			if cpu < 1 {
				cpu = 1
			}
		}
		resolved, resolveErr := llamaruntime.AutoExecutable(context.Background(), *dir, *bin)
		if resolveErr != nil {
			return resolveErr
		}
		managed = &local.Managed{Binary: resolved, ModelPath: *path, Alias: *model, Threads: cpu, ContextTokens: *contextTokens, GPULayers: *gpu, Idle: *idle, MoECPU: *moeCPU, MoECPULayers: *moeCPULayers, MoECacheMiB: *moeCacheMiB}
		engine = managed
	}
	if managed != nil {
		defer managed.Close()
	}
	c, cancel := ctx()
	defer cancel()
	return (&worker.Runner{Client: cli, Engine: engine, Model: *model}).Run(c)
}
func nodes(args []string) error {
	f := fs("nodes")
	server := f.String("server", "https://127.0.0.1:8443", "coordinator")
	ca := f.String("ca", "", "certificate")
	if err := f.Parse(args); err != nil {
		return err
	}
	cli, err := admin(*server, *ca)
	if err != nil {
		return err
	}
	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var items []protocol.Device
	if _, err := cli.Request(c, "GET", "/v1/nodes", nil, &items); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(items)
}
func revoke(args []string) error {
	f := fs("revoke")
	server := f.String("server", "https://127.0.0.1:8443", "coordinator")
	ca := f.String("ca", "", "trusted certificate")
	id := f.String("id", "", "node ID to revoke")
	if err := f.Parse(args); err != nil {
		return err
	}
	if len(*id) != 64 {
		return errors.New("--id requires a 64-character node ID")
	}
	cli, err := admin(*server, *ca)
	if err != nil {
		return err
	}
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = cli.Request(c, http.MethodDelete, "/v1/nodes/"+*id, nil, nil)
	if err == nil {
		fmt.Println("Node revoked:", *id)
	}
	return err
}

func chat(args []string) error {
	f := fs("chat")
	server := f.String("server", "https://127.0.0.1:8443", "coordinator")
	ca := f.String("ca", "", "certificate")
	model := f.String("model", "", "model")
	prompt := f.String("prompt", "", "prompt")
	target := f.String("node", "", "optional target node id")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *model == "" || *prompt == "" {
		return errors.New("--model and --prompt required")
	}
	cli, err := admin(*server, *ca)
	if err != nil {
		return err
	}
	request := protocol.ChatRequest{Model: *model, Messages: []protocol.Message{{Role: "user", Content: *prompt}}, MaxTokens: 512, TargetNode: *target}
	c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var out struct {
		Choices []struct {
			Message protocol.Message `json:"message"`
		} `json:"choices"`
	}
	_, err = cli.Request(c, "POST", "/v1/chat/completions", request, &out)
	if err != nil {
		return err
	}
	if len(out.Choices) == 0 {
		return errors.New("empty chat response")
	}
	fmt.Println(out.Choices[0].Message.Content)
	return nil
}
func admin(server, ca string) (*netclient.Client, error) {
	s := config.AdminSecret(config.DefaultDir())
	if s == "" {
		return nil, errors.New("NODEXLM_ADMIN_TOKEN or a locally initialized host is required")
	}
	if ca == "" {
		localCert := filepath.Join(config.DefaultDir(), "server.crt")
		if _, err := os.Stat(localCert); err == nil {
			ca = localCert
		}
	}
	return makeClient(server, s, ca, "")
}
func models(args []string) error {
	f := fs("models")
	dir := f.String("dir", "./models", "GGUF directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	items, err := local.Models(*dir)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(items)
}

func localChat(args []string) error {
	f := fs("local")
	path := f.String("model", "", "local GGUF file path")
	prompt := f.String("prompt", "", "prompt")
	bin := f.String("llama-bin", "llama-server", "llama-server binary")
	threads := f.Int("threads", 0, "CPU threads")
	gpu := f.String("gpu-layers", "auto", "GPU layers")
	moeCPU := f.Bool("moe-cpu", false, "keep all MoE experts in CPU RAM")
	moeCPULayers := f.Int("moe-cpu-layers", 0, "keep MoE experts of first N layers in CPU RAM")
	moeCacheMiB := f.Int("moe-cache-mib", 0, "GPU cache MiB for CPU-resident MoE experts")
	contextTokens := f.Int("ctx", 2048, "context size")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *path == "" || *prompt == "" {
		return errors.New("--model and --prompt required")
	}
	cpu := *threads
	if cpu <= 0 {
		cpu = runtime.NumCPU() - 1
		if cpu < 1 {
			cpu = 1
		}
	}
	resolved, resolveErr := llamaruntime.AutoExecutable(context.Background(), config.DefaultDir(), *bin)
	if resolveErr != nil {
		return resolveErr
	}
	engine := &local.Managed{Binary: resolved, ModelPath: *path, Alias: filepath.Base(*path), Threads: cpu, ContextTokens: *contextTokens, GPULayers: *gpu, Idle: 0, MoECPU: *moeCPU, MoECPULayers: *moeCPULayers, MoECacheMiB: *moeCacheMiB}
	defer engine.Close()
	c, cancel := ctx()
	defer cancel()
	result, err := engine.Generate(c, protocol.ChatRequest{Model: filepath.Base(*path), Messages: []protocol.Message{{Role: "user", Content: *prompt}}, MaxTokens: 512})
	if err != nil {
		return err
	}
	fmt.Println(result)
	return nil
}

// rpcNode offers native ggml RPC to the enrolled Host through HTTPS tunnels.
// It never exposes the unprotected rpc-server outside 127.0.0.1.
func rpcNode(args []string) error {
	f := fs("rpc-node")
	dir := f.String("dir", config.DefaultDir(), "enrolled node configuration directory")
	bin := f.String("rpc-bin", "", "optional custom rpc-server binary (must match Host's llama.cpp)")
	backend := f.String("backend", "auto", "runtime backend: auto, cpu, vulkan or metal")
	addr := f.String("rpc-addr", "127.0.0.1:50052", "native RPC loopback address")
	slots := f.Int("tunnels", 4, "simultaneous secured RPC tunnels (1-8)")
	ca := f.String("ca", "", "Host certificate for legacy enrollment")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *slots < 1 || *slots > 8 {
		return errors.New("--tunnels must be between 1 and 8")
	}
	if err := rpcmesh.ValidateRPCAddress(*addr); err != nil {
		return err
	}
	agent, err := config.ReadAgent(*dir)
	if err != nil {
		return fmt.Errorf("node not paired: %w", err)
	}
	cli, err := makeClient(agent.Coordinator, agent.Node.Token, *ca, agent.Node.NodeID)
	if err != nil {
		return err
	}
	if agent.CertificateFingerprint != "" && *ca == "" {
		if err = cli.UsePinnedCertificate(agent.CertificateFingerprint); err != nil {
			return err
		}
	}
	if agent.CertificateFingerprint == "" && *ca == "" {
		return errors.New("RPC requires a paired certificate pin or --ca")
	}
	executable := *bin
	if executable == "" {
		c, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
		defer cancel()
		_, executable, err = llamaruntime.EnsureDistributed(c, *dir, *backend)
		if err != nil {
			return err
		}
		if executable == "" {
			return errors.New("llama.cpp runtime does not include an RPC worker; install or build matching ggml-rpc-server with GGML_RPC=ON")
		}
	}
	c, cancel := ctx()
	defer cancel()
	// A lightweight heartbeat informs the Host of real hardware availability.
	name, _ := os.Hostname()
	profile := hardware.Collect(name, nil)
	profile.Runtime.Installed = true
	profile.Runtime.Version = llamaruntime.Version
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			profile = hardware.Collect(name, nil)
			profile.Runtime.Installed = true
			profile.Runtime.Version = llamaruntime.Version
			beatCtx, stop := context.WithTimeout(c, 5*time.Second)
			_, _ = cli.Request(beatCtx, http.MethodPost, "/v1/nodes/heartbeat", profile, nil)
			stop()
			select {
			case <-c.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return rpcmesh.RunNativeGuest(c, cli, executable, *addr, *slots)
}

// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/config"
	"github.com/rfdetoni/nodexlm/internal/coordinator"
	"github.com/rfdetoni/nodexlm/internal/dashboard"
	"github.com/rfdetoni/nodexlm/internal/debuglog"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/llamaruntime"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/mdns"
	"github.com/rfdetoni/nodexlm/internal/netclient"
	"github.com/rfdetoni/nodexlm/internal/pairing"
	"github.com/rfdetoni/nodexlm/internal/protocol"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
	"github.com/rfdetoni/nodexlm/internal/smolmax"
	"github.com/rfdetoni/nodexlm/internal/worker"
)

var version = "0.1.0-alpha"

func main() {
	if os.Getenv("NODEXLM_LOG_FORMAT") == "json" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	}
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
	case "doctor":
		return doctor(args[1:])
	case "bench":
		return bench(args[1:])
	case "cert":
		return cert(args[1:])
	case "web-token":
		return webToken(args[1:])
	case "coordinator":
		return serve(args[1:])
	case "handoff":
		return handoff(args[1:])
	case "host":
		return serve(append([]string{"--pair", "--addr", "0.0.0.0:8443"}, args[1:]...))
	case "join":
		return join(args[1:])
	case "guest":
		return guest(args[1:])
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
	case "image":
		return generateImage(args[1:])
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

  nodexlm doctor [--json]            # diagnose local configuration and runtime
  nodexlm bench --node NODE_ID       # benchmark an idle authenticated Guest tunnel
  nodexlm cert --hosts 127.0.0.1,localhost,192.168.1.10
  nodexlm handoff manifest --source /path/model.gguf # export trusted GGUF hashes
  nodexlm handoff stage --source /mnt/old/model.gguf --manifest handoff.json --target /path/new-host
  nodexlm handoff plan|start|status|cancel|rollback # coordinated epoch-fenced Host switch
  nodexlm host                       # HTTPS host + LAN discovery + Web UI on localhost:8787
  nodexlm host --debug               # private rotating debug logs under NODEXLM_HOME/logs
  nodexlm host --web-addr off        # headless without the Web UI
  nodexlm web-token                  # print local Web UI admin credential
  nodexlm coordinator --addr 0.0.0.0:8443 --pair
  nodexlm join                       # prompts for 8-character code
  nodexlm guest                      # discover Host, pair, start distributed RPC worker
  nodexlm guest --backend cpu        # CPU-only distributed Guest
  nodexlm guest --backend cpu --debug # captures native RPC and connection diagnostics
  nodexlm guest --rejoin --server https://host:8443 --code ABCD2345 # renew stale node credentials
  nodexlm join --code ABCD2345 --name worker-1
  nodexlm pairing # issue a fresh 8-character code
  nodexlm pairing --qr-host https://192.168.1.5:8443 # terminal QR (qrencode)
  nodexlm join --server https://host:8443 --name worker-1 --model model.gguf --ca ./server.crt
  nodexlm node --backend http://127.0.0.1:8080 --model model.gguf --ca ./server.crt
  nodexlm rpc-node                   # shares CPU/GPU for ONE distributed GGUF
  nodexlm nodes --server http://127.0.0.1:8787 --ca ./server.crt
  nodexlm revoke --id NODE_ID --server http://127.0.0.1:8787 --ca ./server.crt
  nodexlm chat --server http://127.0.0.1:8787 --model model.gguf --prompt "Hello" --ca ./server.crt
  nodexlm models --dir ./models
  nodexlm hub search --query "qwen gguf" --json [--server http://127.0.0.1:8787]
  nodexlm hub download --repo owner/repo --file model.Q4_K_M.gguf --dir ./models
  nodexlm hub import --file /path/to/manual.gguf --dir ./models
  nodexlm local --model ./models/model.gguf --prompt "hello"
  nodexlm local --engine litertlm --model registry-id --prompt "hello" # prestarted loopback LiteRT-LM
  nodexlm local --model model.gguf --mmproj projector.gguf --image photo.png --prompt "Describe"
  nodexlm image --model model.safetensors --prompt "A landscape" --output landscape.png # optional sd-cli
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
	debug := f.Bool("debug", false, "log Host events and native llama.cpp output to a private rotating file")
	modelDir := f.String("model-dir", "", "directory containing managed GGUF models")
	rpcTransport := f.String("rpc-transport", "auto", "local RPC transport: auto, unix, tcp")
	llamaBin := f.String("llama-bin", "llama-server", "llama-server executable used by Web UI")
	rpcExclude := f.String("rpc-exclude-ids", "", "comma-separated full Guest IDs to temporarily exclude from RPC model placement (does not revoke pairing)")
	if err := f.Parse(args); err != nil {
		return err
	}
	excludedGuests, err := parseRPCExclusions(*rpcExclude)
	if err != nil {
		return err
	}
	if *debug {
		stop, err := debuglog.Start(*dir, "host")
		if err != nil {
			return fmt.Errorf("enable Host debug logging: %w", err)
		}
		defer stop()
		slog.Debug("Host configuration", "listen", *addr, "web_ui", *webAddr, "rpc_transport", *rpcTransport)
	}
	if len(excludedGuests) != 0 {
		slog.Warn("RPC Guests excluded from model placement for diagnosis", "count", len(excludedGuests))
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
	if *rpcTransport != "auto" && *rpcTransport != "unix" && *rpcTransport != "tcp" {
		return errors.New("invalid rpc-transport")
	}
	if *rpcTransport == "unix" && runtime.GOOS == "windows" {
		return errors.New("Unix RPC transport requires Linux or macOS")
	}
	unixRPC := *rpcTransport == "unix" || *rpcTransport == "auto" && runtime.GOOS != "windows" && llamaruntime.PreferUnixRPC(context.Background(), *llamaBin)
	if unixRPC {
		if err := s.UseUnixRPC(); err != nil {
			return err
		}
	}
	if err := s.SetStore(filepath.Join(*dir, "registry.json")); err != nil {
		return err
	}
	// Model cache is opt-in and shared only through enrolled-node TLS.
	if os.Getenv("NODEXLM_MODEL_P2P") == "1" {
		if *modelDir == "" {
			*modelDir = filepath.Join(*dir, "models")
		}
		if err := s.SetContentCacheDirectory(*modelDir); err != nil {
			return err
		}
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
	srv := &http.Server{Addr: *addr, Handler: traceRoutes(s.LANHandler(), "lan"), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
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
		manager.SetRPCStatsProvider(s.RPCStats)
		s.SetRPCMicrobenchCallbacks(manager.AutoMicrobench, manager.CancelRPCMicrobench)
		manager.SetRPCProvider(func() []dashboard.RPCGuest {
			ready := s.RPCIdleNodes()
			profiles := s.DevicesSnapshot()
			out := make([]dashboard.RPCGuest, 0, len(ready))
			for _, profile := range profiles {
				if _, excluded := excludedGuests[profile.ID]; excluded {
					continue
				}
				endpoint, ok := ready[profile.ID]
				if !ok || profile.Status != "online" || time.Since(profile.LastSeen) > 65*time.Second {
					continue
				}
				memory := clusterplan.RPCMemory(profile)
				out = append(out, dashboard.RPCGuest{
					ID: profile.ID, Name: profile.Name, Endpoint: endpoint,
					CapacityBytes:  clusterplan.Budget(profile),
					RPCDeviceKinds: append([]string(nil), profile.Runtime.RPCDevices...),
					RAMBudgetBytes: memory.RAM, GPUBudgetBytes: memory.GPU,
				})
			}
			return out
		})
		webListener, err = net.Listen("tcp", *webAddr)
		if err != nil {
			return fmt.Errorf("Web UI listen: %w", err)
		}
		defer webListener.Close()
		webUI := dashboard.New(manager, s, adminToken)
		if key := os.Getenv("NODEXLM_API_KEY"); key != "" {
			if key == joinToken {
				return errors.New("API key must differ from join token")
			}
			if err := webUI.SetAPIKey(key); err != nil {
				return err
			}
		}
		webServer = &http.Server{Handler: traceRoutes(webUI.Handler(), "console"), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
		go func() {
			if err := webServer.Serve(webListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("Web UI failed", "error", err)
			}
		}()
		slog.Info("Web management ready", "url", "http://"+webListener.Addr().String(), "auth", "run nodexlm web-token on host")
		_, webPort, _ := net.SplitHostPort(webListener.Addr().String())
		fmt.Printf("\n  Painel Web (somente neste computador): http://localhost:%s\n  Token de acesso: nodexlm web-token\n\n", webPort)
	}
	c, cancel := ctx()
	defer cancel()
	watchAndroidParent(c, cancel)
	go s.Cleanup(c)
	if _, portText, e := net.SplitHostPort(*addr); e == nil {
		if port, e := strconv.Atoi(portText); e == nil && port > 0 && port < 65536 {
			go func() {
				if e := mdns.Serve(c, uint16(port)); e != nil && c.Err() == nil {
					slog.Warn("mDNS unavailable; UDP discovery remains enabled", "error", e)
				}
			}()
		}
	}
	endpoints := pairing.LocalEndpoints(*addr)
	if len(endpoints) > 0 {
		fmt.Println("  Endereços HTTPS do Host para os Guests:")
		for _, address := range endpoints {
			fmt.Println("   ", address)
		}
		fmt.Println()
	}
	go func() {
		if err := pairing.ListenDiscovery(c, *discoveryPort, func() []pairing.Offer { return s.Offers(endpoints) }); err != nil && c.Err() == nil {
			slog.Error("LAN discovery unavailable", "error", err)
		}
	}()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
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
	cancel()
	<-shutdownDone // Keep coordinator and model manager alive through Shutdown.
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
	fingerprint := f.String("fingerprint", "", "optional full Host certificate SHA-256 obtained out of band")
	server := f.String("server", "", "HTTPS coordinator; optional when using --code on LAN")
	code := f.String("code", "", "8-character pairing code displayed by the host")
	rejoin := f.Bool("rejoin", false, "replace existing node credentials after authenticated code pairing")
	discoveryPort := f.Int("discovery-port", pairing.DiscoveryPort, "UDP discovery port")
	ca := f.String("ca", "", "legacy trusted coordinator certificate")
	name := f.String("name", "", "node display name")
	model := f.String("model", "", "optional model name advertised by node")
	dir := f.String("dir", config.DefaultDir(), "configuration directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(*dir, "agent.json")); err == nil {
		if !*rejoin {
			return errors.New("node already enrolled; use --rejoin with a fresh pairing code to replace stale credentials")
		}
		if _, err := config.ReadAgent(*dir); err != nil {
			return fmt.Errorf("cannot replace unsafe or invalid stored Guest credentials: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect stored Guest credentials: %w", err)
	}
	nodeName := *name
	if nodeName == "" {
		nodeName, _ = os.Hostname()
	}
	d := hardware.Collect(nodeName, nil)
	// Record measured Ubuntu RAM/VRAM on initial enrollment, not only after
	// the first RPC heartbeat. Runtime availability is still checked at use.
	if *model != "" {
		d.Models = []string{*model}
	}
	c, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	if *code == "-" || (*code == "" && (*rejoin || *server == "" || os.Getenv("NODEXLM_JOIN_TOKEN") == "" || *model == "")) {
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
				candidateEndpoints, _ = mdns.Discover(c)
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
			if *fingerprint != "" && pairing.VerifyFingerprint(der, *fingerprint) != nil {
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
	if *rejoin {
		return errors.New("--rejoin requires authenticated pairing via --code")
	}
	if *fingerprint != "" {
		return errors.New("--fingerprint requires pairing by code")
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

// traceRoutes logs only route/method in debug mode, never the query string,
// headers, bearer tokens or request/response bodies. Preserves Hijacker.
func traceRoutes(next http.Handler, component string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("HTTP request", "component", component, "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

// guest discovers and securely pairs with the Host before starting a native
// distributed RPC Guest. The code proves TLS identity; no admin token is used.
func guest(args []string) error {
	f := fs("guest")
	fingerprint := f.String("fingerprint", "", "optional full Host certificate SHA-256 obtained out of band")
	device := f.String("rpc-device", "auto", "auto discovers GPU+CPU; specify CPU, Vulkan0 or Vulkan0,CPU to override")
	server := f.String("server", "", "optional Host HTTPS URL; discovery is automatic")
	name := f.String("name", "", "display name of this Guest")
	code := f.String("code", "", "8-character pairing code; prompted when absent")
	rejoin := f.Bool("rejoin", false, "renew stale credentials using a new Host pairing code")
	dir := f.String("dir", config.DefaultDir(), "Guest configuration directory")
	debug := f.Bool("debug", false, "log Guest heartbeat, reconnects and native RPC output to a private rotating file")
	backend := f.String("backend", "auto", "auto, cpu, vulkan, metal or cuda")
	bin := f.String("rpc-bin", "", "matching rpc-server executable")
	threads := f.Int("threads", 0, "Guest CPU threads (0 = automatic)")
	threadPolicy := f.String("thread-policy", "", "auto or performance (big cores; manual --threads takes precedence)")
	address := f.String("rpc-addr", "auto", "private Unix socket with patched runtime, otherwise random loopback TCP")
	tunnels := f.Int("tunnels", 8, "concurrent secured tunnels (1-8)")
	rpcCache := f.Bool("rpc-cache", false, "cache model tensors on this Guest disk (opt-in; may contain model weights)")
	discoveryPort := f.Int("discovery-port", pairing.DiscoveryPort, "LAN UDP discovery port")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("unexpected Guest arguments")
	}
	if *threads < 0 || *threads > 256 {
		return errors.New("--threads must be 0..256")
	}
	if _, err := smolmax.GuestThreads(*threads, hardware.PhysicalCPUCount(), *threadPolicy); err != nil {
		return err
	}
	if *debug {
		stop, err := debuglog.Start(*dir, "guest")
		if err != nil {
			return fmt.Errorf("enable Guest debug logging: %w", err)
		}
		defer stop()
		slog.Debug("Guest configuration", "host", *server, "backend", *backend, "tunnels", *tunnels, "transport", *address)
	}
	if _, err := config.ReadAgent(*dir); err != nil || *rejoin {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("Guest credentials: %w", err)
		}
		if *server == "" {
			fmt.Println("Procurando Hosts na rede local...")
			discoveryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			offers, e := pairing.Discover(discoveryCtx, *discoveryPort, 3*time.Second)
			cancel()
			if e == nil && len(offers) > 0 {
				fmt.Println("Hosts encontrados (a identidade será verificada pelo código):")
				seen := map[string]bool{}
				for _, offer := range offers {
					if !seen[offer.Endpoint] {
						fmt.Println("  " + offer.Endpoint)
						seen[offer.Endpoint] = true
					}
				}
			} else {
				fmt.Println("Nenhum Host detectado; use --server https://IP:8443 se necessário.")
			}
		}
		options := []string{"--dir", *dir, "--discovery-port", fmt.Sprint(*discoveryPort)}
		if *server != "" {
			options = append(options, "--server", *server)
		}
		if *name != "" {
			options = append(options, "--name", *name)
		}
		if *code != "" {
			options = append(options, "--code", *code)
		}
		options = append(options, "--fingerprint", *fingerprint)
		if *rejoin {
			options = append(options, "--rejoin")
		}
		if err := join(options); err != nil {
			return err
		}
	} else {
		fmt.Println("Guest já pareado. Reutilizando credenciais existentes.")
	}
	fmt.Println("Iniciando Guest RPC; Ctrl+C para encerrar.")
	options := []string{"--rpc-device", *device, "--dir", *dir, "--backend", *backend, "--rpc-addr", *address, "--tunnels", fmt.Sprint(*tunnels), "--threads", fmt.Sprint(*threads), "--thread-policy", *threadPolicy}
	if *rpcCache {
		options = append(options, "--rpc-cache")
	}
	if *bin != "" {
		options = append(options, "--rpc-bin", *bin)
	}
	return rpcNode(options)
}
func newPairing(args []string) error {
	f := fs("pairing")
	server := f.String("server", "http://127.0.0.1:8787", "loopback management API")
	ca := f.String("ca", "", "trusted certificate (optional)")
	dir := f.String("dir", config.DefaultDir(), "local coordinator configuration directory")
	qrHost := f.String("qr-host", "", "optional HTTPS LAN endpoint for terminal QR (requires qrencode)")
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
	if *ca == "" && strings.HasPrefix(*server, "https://") {
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
	if *qrHost != "" {
		return printPairingQR(c, *qrHost, result.Code)
	}
	return nil
}

func node(args []string) error {
	f := fs("node")
	backend := f.String("backend", "", "existing localhost inference server (optional)")
	liteRT := f.Bool("litert", false, "verify a local LiteRT-LM registry model before joining as a standalone worker")
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
	decisionModel := f.String("decision-model", "", "typed System One model available on this node (example: ollaya/laya:multilingual)")
	decisionBackend := f.String("decision-backend", "", "local HTTP loopback System One runtime (example: http://127.0.0.1:11435)")
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
	if *model == "" && *decisionModel == "" {
		return errors.New("--model or --decision-model required")
	}
	if (*decisionModel == "") != (*decisionBackend == "") {
		return errors.New("--decision-model and --decision-backend must be configured together")
	}
	var decider worker.DecisionEngine
	if *decisionModel != "" {
		if !coordinator.ValidDecisionModel(*decisionModel) || *decisionModel == "*" {
			return errors.New("invalid --decision-model")
		}
		decider, err = worker.NewLocalDecisionEngine(*decisionBackend)
		if err != nil {
			return err
		}
	}
	if *liteRT && (*backend == "" || *model == "" || *path != "" || *moeCPU || *moeCPULayers != 0 ||
		*moeCacheMiB != 0 || *threads != 0 || *gpu != "auto" || *contextTokens != 2048) {
		return errors.New("--litert requires --backend and --model; configure runtime options in LiteRT-LM")
	}
	var engine worker.Generator
	var managed *local.Managed
	if *model != "" {
		if *backend != "" {
			if *liteRT {
				checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				liteEngine, checkErr := local.NewLiteRT(checkCtx, *backend, *model)
				cancel()
				if checkErr != nil {
					return checkErr
				}
				defer liteEngine.HTTP.CloseIdleConnections()
				engine = liteEngine
			} else {
				engine, err = local.New(*backend)
			}
			if err != nil {
				return err
			}
		} else {
			if *path == "" {
				return errors.New("provide --model-path for lazy loading or --backend for an already running server")
			}
			cpu := *threads
			if cpu <= 0 {
				cpu = hardware.PhysicalCPUCount()
			}
			resolved, resolveErr := llamaruntime.AutoExecutable(context.Background(), *dir, *bin)
			if resolveErr != nil {
				return resolveErr
			}
			managed = &local.Managed{Binary: resolved, ModelPath: *path, Alias: *model, Threads: cpu, ContextTokens: *contextTokens, GPULayers: *gpu, Idle: *idle, MoECPU: *moeCPU, MoECPULayers: *moeCPULayers, MoECacheMiB: *moeCacheMiB}
			engine = managed
		}
	}
	if managed != nil {
		defer managed.Close()
	}
	c, cancel := ctx()
	defer cancel()
	return (&worker.Runner{Client: cli, Engine: engine, Model: *model, DecisionModel: *decisionModel, Decider: decider}).Run(c)
}
func nodes(args []string) error {
	f := fs("nodes")
	server := f.String("server", "http://127.0.0.1:8787", "loopback management API")
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
	server := f.String("server", "http://127.0.0.1:8787", "loopback management API")
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
	server := f.String("server", "http://127.0.0.1:8787", "loopback management API")
	ca := f.String("ca", "", "certificate")
	model := f.String("model", "", "model")
	prompt := f.String("prompt", "", "prompt")
	document := f.String("document", "", "optional local UTF-8 text, DOCX or PDF (max 64 KiB extracted text)")
	target := f.String("node", "", "optional target node id")
	fallbackURL := f.String("fallback-url", "", "explicit HTTPS provider base URL; sends prompt/document externally on local 404/503")
	fallbackModel := f.String("fallback-model", "", "external provider model")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *model == "" || *prompt == "" {
		return errors.New("--model and --prompt required")
	}
	if (*fallbackURL == "") != (*fallbackModel == "") || (*fallbackURL != "" && *target != "") {
		return errors.New("fallback requires URL and model together, and cannot target a Guest")
	}
	content, err := documentPrompt(*prompt, *document)
	if err != nil {
		return err
	}
	cli, err := admin(*server, *ca)
	if err != nil {
		return err
	}
	request := protocol.ChatRequest{Model: *model, Messages: []protocol.Message{{Role: "user", Content: content}}, MaxTokens: 512, TargetNode: *target}
	c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var out struct {
		Choices []struct {
			Message protocol.Message `json:"message"`
		} `json:"choices"`
	}
	status, err := cli.Request(c, "POST", "/v1/chat/completions", request, &out)
	if err != nil {
		if *fallbackURL != "" && (status == 404 || status == 503) {
			fmt.Fprintln(os.Stderr, "Local model unavailable; using explicitly configured external provider.")
			answer, fallbackErr := externalFallback(c, *fallbackURL, *fallbackModel, request.Messages)
			if fallbackErr != nil {
				return fallbackErr
			}
			fmt.Println(answer)
			return nil
		}
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
	path := f.String("model", "", "local GGUF file path or LiteRT-LM registry model ID")
	prompt := f.String("prompt", "", "prompt")
	imagePath := f.String("image", "", "local JPEG/PNG image for vision inference")
	projector := f.String("mmproj", "", "local GGUF vision projector matching the model")
	bin := f.String("llama-bin", "llama-server", "llama-server binary")
	engineName := f.String("engine", "llama.cpp", "inference engine: llama.cpp, experimental mistralrs or litertlm")
	liteEndpoint := f.String("litert-endpoint", "http://127.0.0.1:9379", "already-running local LiteRT-LM server")
	mistralBin := f.String("mistralrs-bin", "", "mistral.rs executable; requires NODEXLM_EXPERIMENTAL_MISTRALRS=1")
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
	if *engineName == "litertlm" {
		if *imagePath != "" || *projector != "" || *moeCPU || *moeCPULayers != 0 || *moeCacheMiB != 0 ||
			*threads != 0 || *gpu != "auto" || *contextTokens != 2048 {
			return errors.New("LiteRT-LM uses its own registry/backend configuration; llama.cpp options are unsupported")
		}
		c, cancel := ctx()
		defer cancel()
		engine, err := local.NewLiteRT(c, *liteEndpoint, *path)
		if err != nil {
			return err
		}
		defer engine.HTTP.CloseIdleConnections()
		result, err := engine.Generate(c, protocol.ChatRequest{Model: *path, Messages: []protocol.Message{{Role: "user", Content: *prompt}}, MaxTokens: 512})
		if err != nil {
			return err
		}
		fmt.Println(result)
		return nil
	}
	if (*imagePath == "") != (*projector == "") {
		return errors.New("--image and --mmproj must be supplied together")
	}
	var dataURL string
	if *imagePath != "" {
		if local.EffectiveBackend(*engineName) != local.BackendLlama {
			return errors.New("vision requires llama.cpp")
		}
		var err error
		dataURL, err = visionDataURL(*imagePath)
		if err != nil {
			return err
		}
	}
	cpu := *threads
	if cpu <= 0 {
		cpu = runtime.NumCPU() - 1
		if cpu < 1 {
			cpu = 1
		}
	}
	resolved := ""
	switch local.EffectiveBackend(*engineName) {
	case local.BackendLlama:
		var resolveErr error
		resolved, resolveErr = llamaruntime.AutoExecutable(context.Background(), config.DefaultDir(), *bin)
		if resolveErr != nil {
			return resolveErr
		}
	case local.BackendMistralRS:
		resolved = *mistralBin
	default:
		return fmt.Errorf("unknown inference engine: %s", *engineName)
	}
	engine := &local.Managed{Backend: *engineName, Binary: resolved, ModelPath: *path, VisionProjectorPath: *projector, Alias: filepath.Base(*path), Threads: cpu, ContextTokens: *contextTokens, GPULayers: *gpu, Idle: 0, MoECPU: *moeCPU, MoECPULayers: *moeCPULayers, MoECacheMiB: *moeCacheMiB}
	defer engine.Close()
	c, cancel := ctx()
	defer cancel()
	if dataURL != "" {
		result, err := generateVision(c, engine, filepath.Base(*path), *prompt, dataURL)
		if err != nil {
			return err
		}
		fmt.Println(result)
		return nil
	}
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
	device := f.String("rpc-device", "auto", "auto discovers GPU+CPU; specify CPU, Vulkan0 or Vulkan0,CPU to override")
	dir := f.String("dir", config.DefaultDir(), "enrolled node configuration directory")
	bin := f.String("rpc-bin", "", "optional custom rpc-server binary (must match Host's llama.cpp)")
	threads := f.Int("threads", 0, "Guest CPU threads (0 = automatic)")
	threadPolicy := f.String("thread-policy", "", "auto or performance (big cores; manual --threads takes precedence)")
	backend := f.String("backend", "auto", "runtime backend: auto, cpu, vulkan, metal or cuda")
	addr := f.String("rpc-addr", "auto", "private Unix socket with patched runtime, otherwise random loopback TCP")
	slots := f.Int("tunnels", 8, "simultaneous secured RPC tunnels (1-8)")
	rpcCache := f.Bool("rpc-cache", false, "cache model tensors on this Guest disk (opt-in; contains model weights)")
	ca := f.String("ca", "", "Host certificate for legacy enrollment")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *threads < 0 || *threads > 256 {
		return errors.New("--threads must be 0..256")
	}
	selectedThreads, err := smolmax.GuestThreads(*threads, hardware.PhysicalCPUCount(), *threadPolicy)
	if err != nil {
		return err
	}
	*threads = selectedThreads
	if *slots < 1 || *slots > 8 {
		return errors.New("--tunnels must be between 1 and 8")
	}
	if *addr != "auto" {
		if err := rpcmesh.ValidateRPCAddress(*addr); err != nil {
			return err
		}
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
	// Reject expired/revoked credentials before fetching or launching a native
	// runtime. A 401 needs a new authenticated pairing, not a CPU/GPU change.
	preflightCtx, preflightCancel := context.WithTimeout(context.Background(), 8*time.Second)
	profile := hardware.Collect(agent.Node.NodeID, nil)
	status, preflightErr := cli.Request(preflightCtx, http.MethodPost, "/v1/nodes/heartbeat", profile, nil)
	preflightCancel()
	if preflightErr != nil {
		if status == http.StatusUnauthorized {
			return fmt.Errorf("Host rejected saved Guest credentials (HTTP 401); get a fresh Host pairing code and run 'nodexlm guest --rejoin --server %s --code NEW_CODE --backend %s': %w", agent.Coordinator, *backend, preflightErr)
		}
		return fmt.Errorf("cannot authenticate Guest with Host; check HTTPS connectivity before starting RPC: %w", preflightErr)
	}
	executable := *bin
	selectedBackend := *backend
	if executable == "" {
		if selectedBackend == "auto" {
			selectedBackend = autoGuestBackend(profile)
			if *device == "CPU" {
				selectedBackend = "cpu"
			}
		}
		buildTimeout := 7 * time.Minute
		if selectedBackend == "cuda" {
			buildTimeout = 60 * time.Minute
		}
		c, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		_, executable, err = llamaruntime.EnsureDistributed(c, *dir, selectedBackend)
		if err != nil && *backend == "auto" && selectedBackend != "cpu" {
			slog.Warn("Auto GPU RPC runtime unavailable; falling back to CPU", "backend", selectedBackend, "error", err)
			selectedBackend = "cpu"
			_, executable, err = llamaruntime.EnsureDistributed(c, *dir, selectedBackend)
		}
		if err != nil {
			return err
		}
		if executable == "" {
			return errors.New("llama.cpp runtime does not include an RPC worker; install or build matching ggml-rpc-server with GGML_RPC=ON")
		}
	}
	if *device == "" || *device == "auto" {
		names := nativeRPCDeviceNames(executable)
		choice := autoRPCDeviceChoice(names, profile)
		if selectedBackend == "cuda" {
			choice, err = explicitCUDADeviceChoice(names, profile)
			if err != nil {
				return err
			}
		}
		if choice == "" && selectedBackend == "cpu" {
			choice = "CPU"
		}
		// An 'auto' Vulkan bundle without a usable native GPU is not
		// an acceleration path. Fall back to the CPU release only when
		// the operator did not explicitly request Vulkan.
		if *backend == "auto" && selectedBackend == "vulkan" &&
			(choice == "" || choice == "CPU") && *bin == "" {
			slog.Warn("Auto Vulkan GPU unavailable; falling back to CPU worker")
			selectedBackend = "cpu"
			c, stop := context.WithTimeout(context.Background(), 7*time.Minute)
			_, executable, err = llamaruntime.EnsureDistributed(c, *dir, selectedBackend)
			stop()
			if err != nil {
				return err
			}
			choice = "CPU"
		}
		*device = choice
	}
	// Native devices are fixed for the lifetime of this worker. If telemetry
	// becomes incomplete, report the same topology and let the Host reject
	// the capacity instead of silently falling back to unknown topology.
	nativeKinds := nativeDeviceKinds(*device, profile)
	if strings.Count(*device, "CUDA") > 1 && len(nativeKinds) == 0 {
		return errors.New("multi-GPU worker requires verified dedicated CUDA topology")
	}
	slog.Info("Guest native device selection", "backend", selectedBackend,
		"rpc_devices", *device, "ram_bytes", profile.AvailableRAMBytes,
		"dedicated_gpu", guestDedicatedGPU(profile))
	c, cancel := ctx()
	defer cancel()
	// A lightweight heartbeat informs the Host of real hardware availability.
	name, _ := os.Hostname()
	heartbeat := func() {
		profile := hardware.Collect(name, nil)
		if *device == "CPU" {
			profile.GPUs = nil
		}
		if *device != "" {
			profile.Runtime.RPCDevices = nativeKinds
		}
		profile.Runtime.Installed = true
		profile.Runtime.Benchmark = true
		profile.Runtime.ReconnectFailures = cli.ReconnectFailures.Load()
		profile.Runtime.Version = llamaruntime.Version
		beatCtx, stop := context.WithTimeout(c, 8*time.Second)
		defer stop()
		_, err := cli.Request(beatCtx, http.MethodPost, "/v1/nodes/heartbeat", profile, nil)
		if err != nil {
			slog.Warn("Guest RAM/VRAM heartbeat failed", "error", err,
				"ram_bytes", profile.RAMBytes, "available_ram_bytes", profile.AvailableRAMBytes,
				"gpus", len(profile.GPUs))
		} else {
			slog.Debug("Guest heartbeat accepted", "available_ram_bytes", profile.AvailableRAMBytes, "ram_bytes", profile.RAMBytes, "gpu_devices", len(profile.GPUs), "runtime", profile.Runtime.Version)
			if profile.RAMBytes == 0 {
				slog.Warn("Ubuntu Guest RAM could not be measured; check /proc/meminfo access")
			}
		}
	}
	// Report immediately, before the first RPC tunnel begins serving layers.
	heartbeat()
	go func() {
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-c.Done():
				return
			case <-tick.C:
				heartbeat()
			}
		}
	}()
	return rpcmesh.RunNativeGuestWithThreads(c, cli, executable, *addr, *slots, *rpcCache, *threads, *device)
}

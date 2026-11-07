package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/config"
	"github.com/rfdetoni/nodexlm/internal/netclient"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

type diagnostic struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Remedy string `json:"remedy,omitempty"`
}

func doctor(args []string) error {
	f := fs("doctor")
	directory := f.String("dir", config.DefaultDir(), "configuration directory")
	jsonOutput := f.Bool("json", false, "machine-readable diagnostics")
	if err := f.Parse(args); err != nil {
		return err
	}
	checks := []diagnostic{{Name: "platform", Status: "ok", Detail: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version()}}
	add := func(name, status, detail, remedy string) {
		checks = append(checks, diagnostic{name, status, detail, remedy})
	}
	info, err := os.Stat(*directory)
	if err != nil {
		add("configuration", "error", err.Error(), "Start nodexlm host or check --dir")
	} else if !info.IsDir() {
		add("configuration", "error", "Configuration path is not a directory", "Choose a directory with --dir")
	} else {
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			add("permissions", "error", info.Mode().String(), "chmod 700 the NodexLM configuration directory")
		} else {
			add("permissions", "ok", "Configuration directory is private", "")
		}
		if free, err := safedownload.FreeBytes(*directory); err != nil {
			add("disk", "error", err.Error(), "Check filesystem permissions")
		} else {
			add("disk", "ok", fmt.Sprintf("%d bytes available", free), "")
		}
	}
	crt, key := filepath.Join(*directory, "server.crt"), filepath.Join(*directory, "server.key")
	if _, err := tls.LoadX509KeyPair(crt, key); err != nil {
		add("tls_identity", "error", err.Error(), "Create a matching identity with nodexlm cert; preserve enrolled identity backups")
	} else {
		raw, _ := os.ReadFile(crt)
		block, _ := pem.Decode(raw)
		cert, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil {
			return parseErr
		}
		sum := sha256.Sum256(cert.Raw)
		status := "ok"
		remedy := ""
		if time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) {
			status = "warning"
			remedy = "Check system clock; CA-based clients require a valid certificate. Paired pins trust the enrolled identity."
		}
		add("certificate", status, fmt.Sprintf("SHA256=%s; valid %s to %s; DNS=%v; IP=%v", hex.EncodeToString(sum[:]), cert.NotBefore.UTC().Format(time.RFC3339), cert.NotAfter.UTC().Format(time.RFC3339), cert.DNSNames, cert.IPAddresses), remedy)
	}
	for _, address := range []string{"127.0.0.1:8443", "127.0.0.1:8787"} {
		c, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err != nil {
			add("tcp_"+address, "warning", "No local listener", "Start the Host or check custom listener flags")
		} else {
			_ = c.Close()
			add("tcp_"+address, "ok", "TCP listener reachable; application identity checked separately", "")
		}
	}
	packet, err := net.ListenPacket("udp", "0.0.0.0:46465")
	if err != nil {
		add("discovery_port", "info", "UDP 46465 is already bound (expected while Host is running)", "If discovery fails, check which process owns the port")
	} else {
		_ = packet.Close()
		add("discovery_port", "info", "UDP 46465 is available", "")
	}
	add("firewall", "unverified", "Local probes cannot establish LAN reachability or firewall policy", "Allow TCP 8443 and UDP 46465 on the trusted LAN; keep TCP 8787 loopback-only")
	if runtime.GOOS == "linux" {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if out, err := exec.CommandContext(c, "getconf", "GNU_LIBC_VERSION").Output(); err == nil {
			add("libc", "info", strings.TrimSpace(string(out)), "Published Linux x64 RPC bundle requires glibc 2.35+; otherwise build from source")
		}
		if cpu, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			status := "warning"
			detail := "AVX2 not detected; use a compatible CPU runtime"
			if strings.Contains(string(cpu), "avx2") {
				status = "ok"
				detail = "AVX2 detected"
			}
			add("avx2", status, detail, "")
		}
	}
	if p, err := exec.LookPath("vulkaninfo"); err == nil {
		add("vulkan", "info", "vulkaninfo available at "+p, "Run vulkaninfo --summary to verify driver/device support")
	} else {
		add("vulkan", "unverified", "vulkaninfo unavailable", "CPU inference remains available; install Vulkan tools to diagnose a GPU")
	}
	if secret := config.AdminSecret(*directory); secret != "" {
		client, _ := netclient.New("http://127.0.0.1:8787", secret, "")
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var cluster json.RawMessage
		if _, err := client.Request(c, http.MethodGet, "/api/cluster", nil, &cluster); err != nil {
			add("cluster", "warning", err.Error(), "Check Host status and the configured local admin credential")
		} else {
			add("cluster", "ok", string(cluster), "")
		}
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(checks)
	}
	for _, check := range checks {
		fmt.Printf("[%s] %s: %s\n", check.Status, check.Name, check.Detail)
		if check.Remedy != "" {
			fmt.Printf("  %s\n", check.Remedy)
		}
	}
	return nil
}

func bench(args []string) error {
	f := fs("bench")
	id := f.String("node", "", "paired Guest node ID")
	server := f.String("server", "http://127.0.0.1:8787", "loopback management URL")
	dir := f.String("dir", config.DefaultDir(), "configuration directory")
	jsonOut := f.Bool("json", false, "JSON output")
	if err := f.Parse(args); err != nil {
		return err
	}
	if len(*id) != 64 || strings.ContainsAny(*id, "/\\") {
		return errors.New("--node requires a paired node ID")
	}
	client, err := netclient.New(*server, config.AdminSecret(*dir), "")
	if err != nil {
		return err
	}
	c, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var result rpcmesh.Measurement
	if _, err := client.Request(c, http.MethodPost, "/v1/bench/"+*id, map[string]any{}, &result); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*dir, "bench-"+*id+".json"), raw, 0600); err != nil {
		return err
	}
	if *jsonOut {
		fmt.Println(string(raw))
	} else {
		fmt.Printf("Guest %s: %.2f ms RTT, %.2f MiB/s aggregate TLS echo throughput\n", result.NodeID, result.RoundTripMS, result.MiBPerSecond)
	}
	return nil
}

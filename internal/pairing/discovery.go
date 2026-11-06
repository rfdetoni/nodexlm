// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package pairing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LocalEndpoints returns coordinator addresses reachable on directly connected IPv4 networks.
func LocalEndpoints(listenAddr string) []string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil
	}
	hosts := []string{}
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		hosts = append(hosts, host)
	} else if host != "" && host != "0.0.0.0" && host != "::" {
		hosts = append(hosts, host)
	} else {
		ifaces, _ := net.Interfaces()
		for _, i := range ifaces {
			if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
					hosts = append(hosts, n.IP.String())
				}
			}
		}
	}
	if len(hosts) == 0 {
		hosts = append(hosts, "127.0.0.1")
	}
	sort.Strings(hosts)
	var endpoints []string
	for j, h := range hosts {
		if j > 0 && hosts[j-1] == h {
			continue
		}
		endpoints = append(endpoints, (&url.URL{Scheme: "https", Host: net.JoinHostPort(h, port)}).String())
	}
	return endpoints
}

// SameLAN is intentionally strict: only directly attached local IPv4 subnets
// may enroll during pairing; loopback is allowed for local testing.
func SameLAN(ip net.IP) bool {
	if ip == nil || ip.To4() == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// ListenDiscovery replies to one-shot UDP broadcast probes on the local LAN.
// It is idle when no active pairing session exists, and never sends the code.
func ListenDiscovery(ctx context.Context, port int, offers func() []Offer) error {
	if port < 1 || port > 65535 {
		return errors.New("invalid discovery port")
	}
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		return err
	}
	defer sock.Close()
	go func() { <-ctx.Done(); _ = sock.Close() }()
	buf := make([]byte, 128)
	for {
		_ = sock.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := sock.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
				continue
			}
			return err
		}
		if !SameLAN(addr.IP) || string(buf[:n]) != DiscoveryMessage {
			continue
		}
		for _, offer := range offers() {
			wire, err := json.Marshal(offer)
			if err != nil {
				continue
			}
			if len(wire) > 1200 {
				continue
			}
			_, _ = sock.WriteToUDP(wire, addr)
		}
	}
}

// Discover broadcasts on each local IPv4 subnet; it also tries loopback for
// single-machine testing. Broadcast discovery may be blocked by WLAN isolation.
func Discover(ctx context.Context, port int, timeout time.Duration) ([]Offer, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("invalid discovery port")
	}
	if timeout <= 0 || timeout > 15*time.Second {
		return nil, errors.New("invalid discovery timeout")
	}
	sock, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer sock.Close()
	if err := sock.SetWriteBuffer(4096); err != nil {
		return nil, err
	}
	targets := []net.IP{net.IPv4(127, 0, 0, 1)}
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ip := n.IP.To4()
			mask := n.Mask
			if len(mask) != net.IPv4len {
				continue
			}
			bcast := make(net.IP, 4)
			for j := 0; j < 4; j++ {
				bcast[j] = ip[j] | ^mask[j]
			}
			targets = append(targets, bcast)
		}
	}
	// UDP broadcast is permitted only for discovery; no credential is sent.
	for _, target := range targets {
		_, _ = sock.WriteToUDP([]byte(DiscoveryMessage), &net.UDPAddr{IP: target, Port: port})
	}
	deadline := time.Now().Add(timeout)
	seen := map[string]bool{}
	var out []Offer
	buf := make([]byte, 1500)
	for len(out) < 32 {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if remaining > 300*time.Millisecond {
			remaining = 300 * time.Millisecond
		}
		_ = sock.SetReadDeadline(time.Now().Add(remaining))
		n, src, err := sock.ReadFromUDP(buf)
		if err != nil {
			if te, ok := err.(net.Error); ok && te.Timeout() {
				continue
			}
			return out, err
		}
		if !SameLAN(src.IP) {
			continue
		}
		var offer Offer
		if json.Unmarshal(buf[:n], &offer) != nil || seen[offer.Endpoint] || time.Now().After(offer.Expires) {
			continue
		}
		u, err := url.Parse(offer.Endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			continue
		}
		host := u.Hostname()
		ip := net.ParseIP(host)
		if ip == nil || !SameLAN(ip) {
			continue
		}
		if _, err := strconv.Atoi(u.Port()); err != nil {
			continue
		}
		seen[offer.Endpoint] = true
		out = append(out, offer)
	}
	sort.Slice(out, func(i, j int) bool { return strings.Compare(out[i].Endpoint, out[j].Endpoint) < 0 })
	return out, nil
}

func SourceIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return nil
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func ListenAddress(host string, port int) string { return fmt.Sprintf("%s:%d", host, port) }

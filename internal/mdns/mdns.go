// Package mdns advertises public discovery metadata only. TLS identity and
// pairing-code proof are still authenticated by the existing pairing protocol.
package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const Host = "nodexlm.local."
const Service = "_nodexlm._tcp.local."

var group = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

func name(v string) dnsmessage.Name { n, _ := dnsmessage.NewName(v); return n }
func response(ips []net.IP, port uint16, ttl uint32) ([]byte, error) {
	instance := name("NodexLM." + Service)
	msg := dnsmessage.Message{Header: dnsmessage.Header{Response: true, Authoritative: true}, Answers: []dnsmessage.Resource{
		{Header: dnsmessage.ResourceHeader{Name: name(Service), Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET, TTL: ttl}, Body: &dnsmessage.PTRResource{PTR: instance}},
		{Header: dnsmessage.ResourceHeader{Name: instance, Type: dnsmessage.TypeSRV, Class: dnsmessage.ClassINET, TTL: ttl}, Body: &dnsmessage.SRVResource{Port: port, Target: name(Host)}},
		{Header: dnsmessage.ResourceHeader{Name: instance, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET, TTL: ttl}, Body: &dnsmessage.TXTResource{TXT: []string{"tls=certificate-pin", "api=v1"}}},
	}}
	for _, ip := range ips {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		var value [4]byte
		copy(value[:], v4)
		msg.Answers = append(msg.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: name(Host), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: ttl}, Body: &dnsmessage.AResource{A: value}})
	}
	return msg.Pack()
}

// Serve fails cleanly when multicast is unavailable or another Host owns the
// shared nodexlm.local alias; callers retain UDP discovery as a fallback.
func Serve(ctx context.Context, port uint16) error {
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	ips := []net.IP{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && ip.To4() != nil && ip.IsPrivate() {
				ips = append(ips, ip)
			}
		}
	}
	if len(ips) == 0 {
		return errors.New("no LAN IPv4 address available for mDNS")
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()
	packed, err := response(ips, port, 120)
	if err != nil {
		return err
	}
	last := time.Time{}
	announce := time.Now()
	buf := make([]byte, 9000)
	for ctx.Err() == nil {
		if time.Since(announce) >= 30*time.Second {
			_, _ = conn.WriteToUDP(packed, group)
			announce = time.Now()
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Timeout() {
				continue
			}
			return err
		}
		if !peer.IP.IsPrivate() && !peer.IP.IsLoopback() {
			continue
		}
		var msg dnsmessage.Message
		if msg.Unpack(buf[:n]) != nil {
			continue
		}
		if msg.Response {
			for _, record := range msg.Answers {
				if strings.EqualFold(record.Header.Name.String(), Host) {
					if a, ok := record.Body.(*dnsmessage.AResource); ok {
						ours := false
						for _, ip := range ips {
							if ip.Equal(net.IP(a.A[:])) {
								ours = true
							}
						}
						if !ours {
							return errors.New("nodexlm.local is already advertised by another Host; using UDP discovery")
						}
					}
				}
			}
			continue
		}
		requested := false
		for _, q := range msg.Questions {
			key := strings.ToLower(q.Name.String())
			if key == Host || key == Service || key == "nodexlm."+Service {
				requested = true
			}
		}
		if !requested || time.Since(last) < 100*time.Millisecond {
			continue
		}
		last = time.Now()
		destination := group
		if peer.Port != 5353 {
			destination = peer
		}
		_, _ = conn.WriteToUDP(packed, destination)
	}
	goodbye, _ := response(ips, port, 0)
	_, _ = conn.WriteToUDP(goodbye, group)
	return nil
}

// Discover issues a bounded unicast-response DNS-SD query. Results are hints,
// never enrolled identities; the caller must verify the code and certificate.
func Discover(ctx context.Context) ([]string, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()
	query := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: name(Service), Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}}}
	raw, _ := query.Pack()
	if _, err = conn.WriteToUDP(raw, group); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	out := []string{}
	seen := map[string]bool{}
	buf := make([]byte, 9000)
	for len(out) < 20 {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		var msg dnsmessage.Message
		if msg.Unpack(buf[:n]) != nil || !msg.Response {
			continue
		}
		port := uint16(0)
		ips := []net.IP{}
		for _, r := range append(msg.Answers, msg.Additionals...) {
			switch body := r.Body.(type) {
			case *dnsmessage.SRVResource:
				if strings.EqualFold(body.Target.String(), Host) {
					port = body.Port
				}
			case *dnsmessage.AResource:
				if strings.EqualFold(r.Header.Name.String(), Host) {
					ip := net.IP(body.A[:])
					if ip.IsPrivate() {
						ips = append(ips, ip)
					}
				}
			}
		}
		if port == 0 {
			continue
		}
		for _, ip := range ips {
			endpoint := fmt.Sprintf("https://%s", net.JoinHostPort(ip.String(), fmt.Sprint(port)))
			if !seen[endpoint] {
				out = append(out, endpoint)
				seen[endpoint] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no mDNS Host found")
	}
	return out, nil
}

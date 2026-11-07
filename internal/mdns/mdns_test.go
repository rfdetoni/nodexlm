package mdns

import (
	"context"
	"golang.org/x/net/dns/dnsmessage"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryRecords(t *testing.T) {
	raw, err := response([]net.IP{net.IPv4(192, 168, 1, 20)}, 8443, 120)
	if err != nil {
		t.Fatal(err)
	}
	var msg dnsmessage.Message
	if err := msg.Unpack(raw); err != nil {
		t.Fatal(err)
	}
	if len(msg.Answers) != 4 || !msg.Response {
		t.Fatal("incomplete DNS-SD response")
	}
	srv, ok := msg.Answers[1].Body.(*dnsmessage.SRVResource)
	if !ok || srv.Port != 8443 || srv.Target.String() != Host {
		t.Fatal("wrong HTTPS endpoint")
	}
}

// Opt in on a real private-LAN interface; the sandbox forbids netlink queries.
func TestLANMulticastDiscovery(t *testing.T) {
	if os.Getenv("NODEXLM_TEST_MDNS") != "1" {
		t.Skip("requires real multicast-capable LAN interface")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exited := make(chan error, 1)
	go func() { exited <- Serve(ctx, 42443) }()
	found := false
	for range 3 {
		select {
		case err := <-exited:
			t.Fatalf("mDNS responder failed: %v", err)
		default:
		}
		endpoints, _ := Discover(ctx)
		for _, endpoint := range endpoints {
			if strings.HasSuffix(endpoint, ":42443") {
				found = true
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Fatal("DNS-SD did not discover the advertised private LAN address")
	}
	cancel()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mDNS responder did not stop")
	}
}

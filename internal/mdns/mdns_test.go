package mdns

import (
	"golang.org/x/net/dns/dnsmessage"
	"net"
	"testing"
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

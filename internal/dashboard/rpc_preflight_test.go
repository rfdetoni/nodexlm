package dashboard

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
)

func TestRPCPreflightDetectsMissingOrUnresponsiveGuest(t *testing.T) {
	candidates := []clusterplan.Candidate{{ID: strings.Repeat("a", 64)}}
	if err := preflightRPCGuests(context.Background(), candidates, nil); err == nil {
		t.Fatal("preflight accepted unmatched Guests and endpoints")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {t.Fatal(err)}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil { return }
		defer conn.Close()
		// A native server can accept a socket but never answer RPC HELLO.
		// The Host must reject it before initiating a distributed model load.
		<-time.After(220*time.Millisecond)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = preflightRPCGuests(ctx, candidates, []string{listener.Addr().String()})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("preflight must fail promptly on unresponsive Guest: %v", err)
	}
	<-done
}

package rpcmesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/netclient"
)

func TestRPCBackoffBoundsAndReset(t *testing.T) {
	if got := nextRPCBackoff(8*time.Second, ErrTunnelCapacity); got != 16*time.Second {
		t.Fatalf("HTTP 503 should permit slower retries: %v", got)
	}
	if got := nextRPCBackoff(28*time.Second, ErrTunnelCapacity); got != 30*time.Second {
		t.Fatalf("503 retry ceiling: %v", got)
	}
	if got := nextRPCBackoff(8*time.Second, errors.New("network failure")); got != 10*time.Second {
		t.Fatalf("ordinary retry ceiling: %v", got)
	}
	if got := nextRPCBackoff(25*time.Second, nil); got != time.Second {
		t.Fatalf("successful relay must clear retry backoff: %v", got)
	}
	for range 100 {
		got := jitteredRPCBackoff(8 * time.Second)
		if got < 8*time.Second || got > 10*time.Second {
			t.Fatalf("retry jitter out of bounds: %v", got)
		}
	}
}

func TestRPCConnectClassifiesRefusalBeforeContactingNative(t *testing.T) {
	for _, tt := range []struct {
		code int
		want error
	}{
		{http.StatusUnauthorized, ErrGuestCredentialsRejected},
		{http.StatusForbidden, ErrGuestCredentialsRejected},
		{http.StatusServiceUnavailable, ErrTunnelCapacity},
	} {
		t.Run(http.StatusText(tt.code), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodConnect {
					t.Errorf("unexpected method: %s", r.Method)
				}
				w.WriteHeader(tt.code)
			}))
			defer server.Close()
			pin := sha256.Sum256(server.TLS.Certificates[0].Certificate[0])
			client, err := netclient.New(server.URL, "secret", "node")
			if err != nil {
				t.Fatal(err)
			}
			if err := client.UsePinnedCertificate(hex.EncodeToString(pin[:])); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if err := Connect(ctx, client, "127.0.0.1:9"); !errors.Is(err, tt.want) {
				t.Fatalf("HTTP %d classified incorrectly: %v", tt.code, err)
			}
		})
	}
}

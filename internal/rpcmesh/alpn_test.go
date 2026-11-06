package rpcmesh

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "errors"
    "io"
    "net"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/rfdetoni/nodexlm/internal/netclient"
)

// Regression: HTTP/2 is allowed for REST, but raw GGML CONNECT must negotiate
// HTTP/1.1 even when the shared client TLS configuration advertises h2.
func TestConnectUsesHTTP11WithHTTP2CapableHost(t *testing.T) {
    received := make(chan string, 1)
    host := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodConnect || r.URL.Path != "/v1/nodes/rpc/tunnel" ||
           r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-Nodex-Node") != "node" {
            http.Error(w, "unexpected tunnel request", http.StatusBadRequest)
            return
        }
        select { case received <- r.Proto: default: }
        hijacker, ok := w.(http.Hijacker)
        if !ok { http.Error(w, "HTTP/1.1 required", http.StatusHTTPVersionNotSupported); return }
        conn, rw, err := hijacker.Hijack()
        if err != nil { return }
        defer conn.Close()
        if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil { return }
        if err = rw.WriteByte(1); err != nil { return }
        if err = rw.Flush(); err != nil { return }
        _, _ = io.Copy(io.Discard, conn)
    }))
    host.EnableHTTP2 = true
    host.StartTLS()
    defer host.Close()

    fingerprint := sha256.Sum256(host.TLS.Certificates[0].Certificate[0])
    client, err := netclient.New(host.URL, "secret", "node")
    if err != nil { t.Fatal(err) }
    if err := client.UsePinnedCertificate(hex.EncodeToString(fingerprint[:])); err != nil { t.Fatal(err) }
    tr := client.HTTP.Transport.(*http.Transport)
    tr.TLSClientConfig.NextProtos = []string{"h2", "http/1.1"}

    local, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil { t.Fatal(err) }
    defer local.Close()
    go func() {
        conn, err := local.Accept()
        if err != nil { return }
        defer conn.Close()
        _, _ = io.Copy(io.Discard, conn)
    }()

    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    err = Connect(ctx, client, local.Addr().String())
    if err != nil && !errors.Is(err, context.Canceled) { t.Fatalf("RPC tunnel failed: %v", err) }
    select {
    case proto := <-received:
        if proto != "HTTP/1.1" { t.Fatalf("CONNECT negotiated %s; HTTP/1.1 required", proto) }
    default:
        t.Fatal("Host did not receive CONNECT")
    }
}

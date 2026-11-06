// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package coordinator

import (
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "io"
    "net"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/rfdetoni/nodexlm/internal/netclient"
    "github.com/rfdetoni/nodexlm/internal/protocol"
    "github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

// Validates the real Host coordinator route with TLS, node credentials,
// HTTP/2-capable REST, forced HTTP/1.1 CONNECT and the binary GGML relay.
func TestAuthenticatedRPCTunnelThroughCoordinator(t *testing.T) {
    s,err:=New(adminSecret,joinSecret)
    if err!=nil {t.Fatal(err)}
    defer s.CloseRPC()
    joined:=call(t,s.Handler(),http.MethodPost,"/v1/nodes/enroll",joinSecret,"",
        protocol.Enrollment{Device:protocol.Device{Name:"linux-guest",Platform:"linux",
            CPUCount:4,RAMBytes:8<<30,AvailableRAMBytes:6<<30}})
    if joined.Code!=201 {t.Fatalf("enroll: %d %s",joined.Code,joined.Body.String())}
    var cred protocol.Credential
    if err:=json.Unmarshal(joined.Body.Bytes(),&cred);err!=nil {t.Fatal(err)}
    if nodes:=s.DevicesSnapshot();len(nodes)!=1 || nodes[0].Runtime.RPCReady {
        t.Fatalf("enrolled node must not be RPC ready: %+v",nodes)
    }

    local,err:=net.Listen("tcp","127.0.0.1:0")
    if err!=nil {t.Fatal(err)}
    defer local.Close()
    go func() {
        for {
            c,e:=local.Accept()
            if e!=nil {return}
            go func(){defer c.Close();_,_=io.Copy(c,c)}()
        }
    }()
    host:=httptest.NewUnstartedServer(s.Handler())
    host.EnableHTTP2=true
    host.StartTLS()
    defer host.Close()
    pin:=sha256.Sum256(host.TLS.Certificates[0].Certificate[0])
    client,err:=netclient.New(host.URL,cred.Token,cred.NodeID)
    if err!=nil {t.Fatal(err)}
    if err:=client.UsePinnedCertificate(hex.EncodeToString(pin[:]));err!=nil {t.Fatal(err)}
    ctx,cancel:=context.WithTimeout(context.Background(),8*time.Second)
    defer cancel()

    _,err=client.Request(ctx,http.MethodPost,"/v1/nodes/heartbeat",
        protocol.Device{Name:"linux-guest",Platform:"linux",CPUCount:4,
            RAMBytes:8<<30,AvailableRAMBytes:6<<30},nil)
    if err!=nil {t.Fatal(err)}
    exited:=make(chan error,1)
    go func(){exited<-rpcmesh.Connect(ctx,client,local.Addr().String())}()
    var endpoint string
    for i:=0;i<150;i++ {
        endpoint=s.RPCNodes()[cred.NodeID]
        if endpoint!="" {break}
        select {case e:=<-exited:t.Fatalf("RPC tunnel exited before ready: %v",e);default:}
        time.Sleep(10*time.Millisecond)
    }
    if endpoint=="" {t.Fatal("no authenticated RPC tunnel reached the coordinator")}
    ready:=s.DevicesSnapshot()
    if len(ready)!=1 || !ready[0].Runtime.RPCReady {
        t.Fatalf("live authenticated RPC tunnel not reflected in node: %+v",ready)
    }
    conn,err:=net.DialTimeout("tcp",endpoint,time.Second)
    if err!=nil {t.Fatal(err)}
    defer conn.Close()
    _=conn.SetDeadline(time.Now().Add(3*time.Second))
    payload:=[]byte("ggml layer bytes")
    if _,err=conn.Write(payload);err!=nil {t.Fatal(err)}
    recv:=make([]byte,len(payload))
    if _,err=io.ReadFull(conn,recv);err!=nil {t.Fatal(err)}
    if string(recv)!=string(payload) {t.Fatalf("RPC relay corrupted data: %q",recv)}
}

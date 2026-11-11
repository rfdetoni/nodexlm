package dashboard

import (
    "testing"
    "time"
    "github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

func TestLoadStateUsesOnlyMeasuredRPCBytesFromCurrentLoad(t *testing.T) {
    m, err := NewManager(t.TempDir(), t.TempDir(), "")
    if err != nil { t.Fatal(err) }
    defer m.Close()
    now:=time.Now()
    samples:=map[string]rpcmesh.TunnelStats{"guest":{
        BytesToGuest:1200, BytesFromGuest:300, Connected:2, Idle:1,
        LastActivityMS:now.UnixMilli(),
    }}
    m.SetRPCStatsProvider(func()map[string]rpcmesh.TunnelStats{return samples})
    m.mu.Lock()
    m.loading=true
    m.loadStartedAt=now.Add(-15*time.Second)
    m.phaseStartedAt=now.Add(-10*time.Second)
    m.loadPhase="loading_model"
    m.loadRPCNodes=[]string{"guest"}
    m.loadRPCBaseline=map[string]rpcmesh.TunnelStats{"guest":{
        BytesToGuest:1000,BytesFromGuest:200,
    }}
    m.mu.Unlock()
    state:=m.State()
    activity,ok:=state["rpc_transfer"].(map[string]map[string]any)
    if !ok {t.Fatalf("missing RPC transfer: %+v",state)}
    node:=activity["guest"]
    if node["to_guest_bytes"]!=uint64(200)||node["from_guest_bytes"]!=uint64(100){
        t.Fatalf("reported cumulative bytes instead of this load: %+v",node)
    }
    if node["connected"]!=2||node["idle"]!=1 {
        t.Fatalf("lost tunnel state: %+v",node)
    }
}

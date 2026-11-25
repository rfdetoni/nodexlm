// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/rpcmesh"
)

func TestLoadedTopologyUsesNativeSplitAndWeightMetadata(t *testing.T) {
	meta := local.GGUFInfo{BlockCount: 4, BlockBytes: []uint64{10, 20, 30, 40}, NonBlockBytes: 5}
	guest := RPCGuest{ID: "guest", Name: "Guest", RPCDeviceKinds: []string{"cpu"}, RAMBudgetBytes: 100}
	selection := clusterplan.Selection{Guests: []clusterplan.Candidate{{ID: guest.ID, CapacityBytes: 100}}, LocalLayers: 2}
	engine := &local.Managed{RPCServers: []string{"127.0.0.1:42"}, RPCLocalLayers: 2, RPCDeviceNames: []string{"RPC0"}}
	topology := buildInferenceTopology(Settings{Model: "model.gguf"}, meta, 105, 100, selection, []RPCGuest{guest}, RPCDevicePlacement{Devices: []string{"RPC0"}, Split: []uint64{2}, Modes: map[string]string{"guest": "ram"}}, engine)
	if len(topology.Nodes) != 2 || topology.Nodes[0].WeightBytes != 35 || topology.Nodes[1].WeightBytes != 70 || topology.Nodes[1].FirstBlock != 2 || topology.Nodes[1].LastBlock != 3 {
		t.Fatalf("invalid loaded topology: %+v", topology.Nodes)
	}
}

func TestActivityWindowRejectsEpochAndOffline(t *testing.T) {
	nodes := []topologyNode{{NodeID: "host"}, {NodeID: "a"}, {NodeID: "b"}}
	now := time.Now()
	before := activityObservation{at: now.Add(-time.Second), stats: map[string]rpcmesh.TunnelStats{"a": {Epoch: 1, Connected: 1, BytesToGuest: 100}, "b": {Epoch: 2, Connected: 1, BytesToGuest: 100}}}
	after := activityObservation{at: now, stats: map[string]rpcmesh.TunnelStats{"a": {Epoch: 1, Connected: 1, BytesToGuest: 400, WritesToGuest: 2}, "b": {Epoch: 2, Connected: 1, BytesToGuest: 200}}}
	rows, host := calculateActivity(nodes, before, before, after)
	if host || len(rows) != 2 || rows[0].BytesDelta != 300 || rows[0].SharePercent != 75 || rows[1].SharePercent != 25 || !rows[0].Active {
		t.Fatalf("invalid activity: %+v %v", rows, host)
	}
	after.stats["a"] = rpcmesh.TunnelStats{Epoch: 3, Connected: 1, BytesToGuest: 900}
	after.stats["b"] = rpcmesh.TunnelStats{Epoch: 2, Connected: 0, BytesToGuest: 100}
	rows, host = calculateActivity(nodes, before, before, after)
	if host || rows[0].Available || rows[1].Available {
		t.Fatalf("reset/offline counted as traffic or Host activity: %+v %v", rows, host)
	}
	after.stats["a"] = before.stats["a"]
	after.stats["b"] = before.stats["b"]
	_, host = calculateActivity(nodes, before, before, after)
	if !host {
		t.Fatal("valid zero remote delta did not identify inferred Host")
	}
}

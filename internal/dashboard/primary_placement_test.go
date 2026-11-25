// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"testing"

	"github.com/rfdetoni/nodexlm/internal/local"
)

func TestExplicitPrimaryPlacement(t *testing.T) {
	meta := local.GGUFInfo{BlockCount: 8, BlockBytes: []uint64{100, 100, 100, 100, 100, 100, 100, 100}, NonBlockBytes: 10}
	guests := []RPCGuest{{ID: "a", CapacityBytes: 1000, RAMBudgetBytes: 1000, RPCDeviceKinds: []string{"cpu"}}, {ID: "b", CapacityBytes: 460, RAMBudgetBytes: 460, RPCDeviceKinds: []string{"cpu"}}}
	s := Settings{PrimaryNode: "a", Distributed: true, GPULayers: "auto", ContextTokens: 256}
	r, err := choosePrimary(s, meta, 810, 1000, guests)
	if err != nil || r.LocalLayers != 0 || len(r.Guests) != 1 || r.GuestLayers[0] != 8 {
		t.Fatalf("whole guest: %+v %v", r, err)
	}
	s.PlacementStrategy = "capacity"
	r, err = choosePrimary(s, meta, 810, 1000, guests)
	if err != nil || len(r.Guests) != 2 || r.GuestLayers[0] != 7 || r.GuestLayers[1] != 1 {
		t.Fatalf("capacity primary: %+v %v", r, err)
	}
	s.PrimaryNode = "host"
	r, err = choosePrimary(s, meta, 810, 1000, guests)
	if err != nil || r.LocalLayers != 6 || len(r.Guests) != 2 {
		t.Fatalf("host capacity: %+v %v", r, err)
	}
	s.PrimaryNode = "offline"
	if _, err = choosePrimary(s, meta, 810, 1000, guests); err == nil {
		t.Fatal("offline primary accepted")
	}
	s.PrimaryNode, s.PlacementStrategy = "a", "speed"
	guests[0].CapacityBytes, guests[0].RAMBudgetBytes = 230, 230
	r, err = choosePrimary(s, meta, 810, 1000, guests)
	if err != nil || r.GuestLayers[0] != 2 || r.LocalLayers == 0 {
		t.Fatalf("limited primary: %+v %v", r, err)
	}
}

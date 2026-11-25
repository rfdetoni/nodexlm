// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package clusterplan

import "testing"

func TestLayerAllocationNonUniform(t *testing.T) {
	got, ok := LayerAllocation([]uint64{20, 80, 20, 20}, 10, 60, []uint64{150}, 2)
	if !ok || got[0] != 1 || got[1] != 3 {
		t.Fatalf("nonuniform allocation: %v %v", got, ok)
	}
	if _, ok := LayerAllocation([]uint64{20, 80, 20, 20}, 10, 20, []uint64{100}, 2); ok {
		t.Fatal("unsafe allocation accepted")
	}
}

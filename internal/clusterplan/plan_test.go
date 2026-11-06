// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package clusterplan

import "testing"

func TestLocalLayerPlacement(t *testing.T) {
	const gib = uint64(1 << 30)
	for _, tt := range []struct {
		name        string
		blocks      uint32
		bytes, host uint64
		guest       []uint64
		want        int
		fail        bool
	}{
		{"balanced", 40, 7 * gib, 8 * gib, []uint64{8 * gib}, 20, false},
		{"guest_only", 40, 5 * gib, 0, []uint64{8 * gib}, 0, false},
		{"host_dominant_still_uses_guest", 8, gib, 30 * gib, []uint64{gib}, 7, false},
		{"insufficient", 32, 8 * gib, 0, []uint64{2 * gib}, 0, true},
		{"unmeasured_guest", 8, gib, 8 * gib, []uint64{0}, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LocalLayerCount(tt.blocks, tt.bytes, tt.host, tt.guest)
			if (err != nil) != tt.fail || !tt.fail && got != tt.want {
				t.Fatalf("got=%d err=%v; want=%d fail=%v", got, err, tt.want, tt.fail)
			}
		})
	}
}

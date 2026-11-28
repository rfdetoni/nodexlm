package main

import "testing"

func TestGuestUpdateChannelRejectsDowngradeAndArbitraryTarget(t *testing.T) {
	for _, pair := range [][2]string{
		{"0.1.0-edge.108", "0.1.0-edge.107"},
		{"0.1.0-edge.108", "0.1.0-edge.108"},
		{"1.1.0", "0.1.0-edge.109"},
		{"0.1.0-alpha", "../../other"},
		{"1.1.0", "1.0.0"},
	} {
		if _, err := guestUpdateTag(pair[0], pair[1]); err == nil {
			t.Fatalf("accepted update %q -> %q", pair[0], pair[1])
		}
	}
	if tag, err := guestUpdateTag("0.1.0-edge.108", "0.1.0-edge.109"); tag != "nodexlm-edge" || err != nil {
		t.Fatalf("edge update rejected: %s %v", tag, err)
	}
}

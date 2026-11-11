package main

import (
	"strings"
	"testing"
)

func TestParseRPCExclusions(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, raw := range []string{"", "   "} {
		got, err := parseRPCExclusions(raw)
		if err != nil || len(got) != 0 {
			t.Fatalf("empty selection: %v %v", got, err)
		}
	}
	got, err := parseRPCExclusions("  " + a + " , " + b + ", " + a + " ")
	if err != nil || len(got) != 2 {
		t.Fatalf("unexpected exclusions: %v %v", got, err)
	}
	for _, id := range []string{a, b} {
		if _, ok := got[id]; !ok {
			t.Fatalf("lost configured Guest %s", id)
		}
	}
	for _, raw := range []string{"e577cb5d", a[:63], strings.ToUpper(a), strings.Repeat("z", 64), a + ",", "," + a} {
		if _, err := parseRPCExclusions(raw); err == nil {
			t.Fatalf("unsafe exclusion accepted: %q", raw)
		}
	}
}

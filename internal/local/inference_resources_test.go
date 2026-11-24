package local

import (
	"slices"
	"testing"
)

func TestPinnedResourceArguments(t *testing.T) {
	m := &Managed{ThreadsBatch: 8, PromptCacheMiB: 256, LoadMode: "mmap", LazyMode: "on", NUMA: "distribute"}
	args := m.args(9999, 2048, 4, "0", 0)
	for flag, value := range map[string]string{"--parallel": "1", "--cache-ram": "256", "--threads-batch": "8", "--load-mode": "mmap", "--lazy-mode": "on", "--numa": "distribute"} {
		i := slices.Index(args, flag)
		if i < 0 || i+1 == len(args) || args[i+1] != value {
			t.Fatalf("missing %s %s in %v", flag, value, args)
		}
	}
}

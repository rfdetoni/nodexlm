package local

import (
	"strings"
	"testing"
)

func TestNativeSystemInfoIsBoundedAndRetained(t *testing.T) {
	log := &nativeLogTail{}
	_, _ = log.Write([]byte("prefix system_info: n_threads = "))
	_, _ = log.Write([]byte("4 | AVX2 = 1 |\n"))
	_, _ = log.Write([]byte(strings.Repeat("x", 8192)))
	if got := log.SystemInfo(); got != "system_info: n_threads = 4 | AVX2 = 1 |" {
		t.Fatalf("unexpected native capability %q", got)
	}
}

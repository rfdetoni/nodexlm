package llamaruntime

import (
	"strings"
	"testing"
)

func TestValidateArchitecture(t *testing.T) {
	for _, arch := range []string{"", "llama", "inkling2", "inkling2_loop"} {
		if err := ValidateArchitecture(arch); err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
	}
	if err := ValidateArchitecture("nodexlm_unknown_arch"); err == nil || !strings.Contains(err.Error(), Version) {
		t.Fatalf("missing unsupported architecture diagnostic: %v", err)
	}
}

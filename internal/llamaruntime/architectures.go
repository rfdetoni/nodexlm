package llamaruntime

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed architectures.txt
var architectureNames string

// ValidateArchitecture checks the names registered by the pinned native
// source, not whether a particular quantization or backend can load it.
func ValidateArchitecture(architecture string) error {
	if architecture == "" {
		return nil
	}
	for _, name := range strings.Fields(architectureNames) {
		if name == architecture {
			return nil
		}
	}
	return fmt.Errorf("GGUF architecture %q is not recognized by bundled llama.cpp %s; update NodexLM or select a compatible custom runtime", architecture, Version)
}

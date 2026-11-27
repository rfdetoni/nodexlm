package llamaruntime

import (
	"errors"
	"fmt"
)

// ggmlTypeCount and removed IDs mirror ggml/include/ggml.h at SourceCommit.
// This is a format admission check, not a guarantee of backend kernel support.
const ggmlTypeCount = 44

var ErrTrellisFormat = errors.New("Trellis GGUF requires a separate native runtime")

func ValidateTrellisTensorType(kind uint32, tensor string) error {
	if kind >= 144 && kind <= 149 {
		return nil
	}
	return ValidateTensorType(kind, tensor)
}

func ValidateTensorType(kind uint32, tensor string) error {
	if kind >= 144 && kind <= 149 {
		name := [...]string{"TQ2_T", "TQK6", "TQK7", "TQP5", "TQP6", "TQP7"}[kind-144]
		return fmt.Errorf("%w: tensor %q uses GGML type %d (%s); stock llama.cpp %s is incompatible", ErrTrellisFormat, tensor, kind, name, Version)
	}
	switch kind {
	case 4, 5, 31, 32, 33, 36, 37, 38:
		return fmt.Errorf("tensor %q uses removed GGML type %d; use a compatible GGUF", tensor, kind)
	}
	if kind >= ggmlTypeCount {
		return fmt.Errorf("tensor %q uses unsupported GGML type %d; bundled llama.cpp %s supports IDs below %d; use a compatible GGUF or custom runtime", tensor, kind, Version, ggmlTypeCount)
	}
	return nil
}

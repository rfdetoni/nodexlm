package llamaruntime

import "fmt"

// ggmlTypeCount and removed IDs mirror ggml/include/ggml.h at SourceCommit.
// This is a format admission check, not a guarantee of backend kernel support.
const ggmlTypeCount = 44

func ValidateTensorType(kind uint32, tensor string) error {
	if kind == 144 {
		return fmt.Errorf("tensor %q uses GGML type 144 (Agention TQ2_T/Gyro-M); bundled llama.cpp %s does not support it; use a standard GGUF or a compatible custom runtime (distributed Guests must also support the format)", tensor, Version)
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

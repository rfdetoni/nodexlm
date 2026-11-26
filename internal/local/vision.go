package local

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateVisionProjector checks a local CLIP projector without loading weights.
func ValidateVisionProjector(projector, model string) error {
	if !strings.EqualFold(filepath.Ext(projector), ".gguf") {
		return errors.New("vision projector must be a GGUF file")
	}
	p, err := os.Lstat(projector)
	if err != nil {
		return err
	}
	if !p.Mode().IsRegular() {
		return errors.New("vision projector must be a regular file")
	}
	m, err := os.Stat(model)
	if err != nil {
		return err
	}
	if os.SameFile(p, m) {
		return errors.New("vision projector must differ from the language model")
	}
	meta, err := InspectGGUF(projector)
	if err != nil {
		return fmt.Errorf("invalid vision projector: %w", err)
	}
	if meta.Architecture != "clip" {
		return errors.New("vision projector must have GGUF clip architecture")
	}
	return nil
}

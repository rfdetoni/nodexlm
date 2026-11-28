package local

import "context"

// NewLiteRT keeps the original API while sharing the registry admission path.
func NewLiteRT(ctx context.Context, endpoint, model string) (*Engine, error) {
	return NewRegisteredRuntime(ctx, "litertlm", endpoint, model)
}

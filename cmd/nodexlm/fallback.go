package main

import (
	"context"
	"os"

	"github.com/rfdetoni/nodexlm/internal/external"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func externalFallback(ctx context.Context, endpoint, model string, messages []protocol.Message) (string, error) {
	return external.Chat(ctx, endpoint, model, os.Getenv("NODEXLM_FALLBACK_API_KEY"), messages, 512)
}

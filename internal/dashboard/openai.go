package dashboard

import (
	"context"
	"io"
	"net/http"

	"github.com/rfdetoni/nodexlm/internal/local"
	compat "github.com/rfdetoni/nodexlm/internal/openai"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func (m *Manager) openAI(ctx context.Context, model, path string, body []byte, receive func(int, http.Header, io.Reader) error) error {
	if !m.HasModel(model) {
		return &compat.APIError{Status: 404, Message: "Requested model is not loaded", Param: "model", Code: "model_not_found"}
	}
	return m.withGeneration(ctx, protocol.ChatRequest{Model: model}, func(ctx context.Context, engine *local.Managed, _ protocol.ChatRequest) error {
		return engine.OpenAI(ctx, path, body, receive)
	})
}

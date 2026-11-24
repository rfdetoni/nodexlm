package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/local"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestParallelAdmissionAndUnloadCancellation(t *testing.T) {
	m := &Manager{engine: &local.Managed{}, current: "model", admission: make(chan struct{}, 2)}
	started := make(chan struct{}, 2)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			done <- m.withGeneration(context.Background(), protocol.ChatRequest{Model: "model"}, func(ctx context.Context, _ *local.Managed, _ protocol.ChatRequest) error {
				started <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			})
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("parallel requests did not start")
		}
	}
	m.Unload()
	for i := 0; i < 2; i++ {
		if err := <-done; err != context.Canceled {
			t.Fatalf("expected cancellation, got %v", err)
		}
	}
}

func TestParallelKVAccountsForEachContext(t *testing.T) {
	meta := local.GGUFInfo{BlockCount: 24, EmbeddingLength: 4096, HeadCount: 32, KVHeadCount: 8}
	s := Settings{ContextTokens: 2048}
	one := estimateKVBytes(meta, s)
	s.ParallelSlots = 4
	if four := estimateKVBytes(meta, s); one == nil || four == nil || *four != *one*4 {
		t.Fatal("incorrect parallel KV estimate")
	}
}

package local

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rfdetoni/nodexlm/internal/protocol"
)

func TestStreamIsIncrementalAndCancellable(t *testing.T) {
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(disconnected)
	}))
	defer server.Close()
	engine, _ := New(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := engine.GenerateStream(ctx, protocol.ChatRequest{}, func(frame []byte) error {
		if !strings.Contains(string(frame), "first") {
			t.Errorf("unexpected frame %q", frame)
		}
		cancel()
		return nil
	})
	if err == nil {
		t.Fatal("incomplete stream accepted")
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("upstream generation not cancelled")
	}
}
func TestStreamRequiresDone(t *testing.T) {
	for _, end := range []string{"", "data: [DONE]\n\n"} {
		t.Run(fmt.Sprint(len(end)), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[]}\n\n"+end)
			}))
			defer s.Close()
			engine, _ := New(s.URL)
			err := engine.GenerateStream(context.Background(), protocol.ChatRequest{}, func([]byte) error { return nil })
			if (err == nil) != (end != "") {
				t.Fatal(err)
			}
		})
	}
}

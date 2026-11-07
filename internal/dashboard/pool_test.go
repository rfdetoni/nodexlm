package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrimaryLoadRechecksPoolLimitAfterWaiting(t *testing.T) {
	_, m := newTestUI(t)
	if err := os.WriteFile(filepath.Join(m.modelDir, "fifth.gguf"), []byte("GGUF"), 0600); err != nil {
		t.Fatal(err)
	}
	m.settings.Model = "fifth.gguf"
	if err := m.beginLoad(nil); err != nil {
		t.Fatal(err)
	}
	// An additional model can finish between beginLoad and poolMu admission.
	m.residents = map[string]*Manager{"one": {}, "two": {}, "three": {}, "four": {}}
	if err := m.runLoad(context.Background()); err == nil || !strings.Contains(err.Error(), "four models") {
		t.Fatalf("fifth resident accepted: %v", err)
	}
	if m.loading || m.engine != nil {
		t.Fatal("rejected load left active state")
	}
	if err := m.beginLoad(nil); err == nil {
		t.Fatal("full pool admitted another primary load")
	}
}

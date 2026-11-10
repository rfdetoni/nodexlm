package dashboard

import (
    "os"
    "path/filepath"
    "testing"
)

func TestResolveDraftModelAcceptsOnlyCompleteLocalGGUF(t *testing.T) {
    root := t.TempDir()
    m, err := NewManager(filepath.Join(root, "state"), filepath.Join(root, "models"), "")
    if err != nil { t.Fatal(err) }
    defer m.Close()
    model := filepath.Join(root, "models", "draft.gguf")
    data := make([]byte, 24)
    copy(data, "GGUF")
    if err := os.WriteFile(model, data, 0600); err != nil { t.Fatal(err) }
    got, err := m.resolveDraftModel(Settings{Model:"main.gguf", SpeculativeMode:"draft", DraftModel:"draft.gguf"})
    if err != nil || got != model { t.Fatalf("got %q err %v", got, err) }
    for _, name := range []string{"../outside.gguf","abs/path.gguf","missing.gguf","main.gguf"} {
        _, err := m.resolveDraftModel(Settings{Model:"main.gguf", SpeculativeMode:"draft", DraftModel:name})
        if err == nil { t.Fatalf("accepted draft model %q", name) }
    }
    invalid := filepath.Join(root, "models", "invalid.gguf")
    if err := os.WriteFile(invalid, []byte("not gguf"), 0600); err != nil { t.Fatal(err) }
    if _, err := m.resolveDraftModel(Settings{Model:"main.gguf", SpeculativeMode:"draft", DraftModel:"invalid.gguf"}); err == nil {
        t.Fatal("accepted corrupted GGUF draft")
    }
}

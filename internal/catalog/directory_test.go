package catalog

import (
    "context"
    "os"
    "path/filepath"
    "testing"
)

func TestImportDirectoryLinksCompleteSet(t *testing.T) {
    src, dst := t.TempDir(), t.TempDir()
    for _, name := range []string{"model-00001-of-00002.gguf", "model-00002-of-00002.gguf", "single.gguf"} {
        if err := os.WriteFile(filepath.Join(src,name), []byte("GGUFtest"), 0600); err != nil { t.Fatal(err) }
    }
    got, err := ImportDirectory(context.Background(), src, dst, true)
    if err != nil || len(got) != 2 { t.Fatalf("%v %v",got,err) }
    for _, p := range got {
        a, e := os.Stat(p); if e != nil { t.Fatal(e) }
        b, e := os.Stat(filepath.Join(src,filepath.Base(p))); if e != nil { t.Fatal(e) }
        if !os.SameFile(a,b) { t.Fatal("weights copied instead of linked") }
    }
    if _, err := ImportDirectory(context.Background(), src, dst, true); err == nil { t.Fatal("overwrote an existing model") }
}

func TestImportDirectoryIncompleteSet(t *testing.T) {
    src, dst := t.TempDir(), t.TempDir()
    if err := os.WriteFile(filepath.Join(src,"model-00001-of-00002.gguf"), []byte("GGUFtest"), 0600); err != nil { t.Fatal(err) }
    if _, err := ImportDirectory(context.Background(), src, dst, true); err == nil { t.Fatal("accepted missing shard") }
    entries, err := os.ReadDir(dst)
    if err != nil || len(entries) != 0 { t.Fatalf("unexpected partial model: %v %v",entries,err) }
}

func TestImportDirectoryRejectsInvalidHeader(t *testing.T) {
    src, dst := t.TempDir(), t.TempDir()
    if err := os.WriteFile(filepath.Join(src,"model.gguf"), []byte("bad!"), 0600); err != nil { t.Fatal(err) }
    if _, err := ImportDirectory(context.Background(), src, dst, true); err == nil { t.Fatal("accepted invalid header") }
}

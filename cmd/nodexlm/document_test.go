package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("dados úteis"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := documentPrompt("Resuma", path)
	if err != nil || !strings.Contains(got, "dados úteis") || !strings.HasPrefix(got, "Resuma") {
		t.Fatalf("%q %v", got, err)
	}
	if err := os.WriteFile(path, make([]byte, maxChatDocument+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := documentPrompt("Resuma", path); err == nil {
		t.Fatal("accepted oversized document")
	}
	if _, err := documentPrompt("Resuma", "https://example.org/file.pdf"); err == nil {
		t.Fatal("accepted nontext source")
	}
}

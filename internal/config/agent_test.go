package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadAgentRejectsExposedCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses ACLs")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(path, []byte(`{"coordinator":"https://host","node":{"token":"private"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAgent(dir); err == nil {
		t.Fatal("accepted world-readable node token")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAgent(dir); err != nil {
		t.Fatal(err)
	}
}

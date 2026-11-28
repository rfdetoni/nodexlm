package llamaruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrellisInstallIntegrity(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"source-commit.txt":   TrellisSourceCommit,
		"LICENSE":             "license",
		"bin/llama-server":    "server",
		"bin/ggml-rpc-server": "worker",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if verifyTrellisInstall(root) == nil {
		t.Fatal("unsealed install accepted")
	}
	if err := sealTrellisInstall(root); err != nil {
		t.Fatal(err)
	}
	if err := verifyTrellisInstall(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "llama-server"), []byte("edited"), 0600); err != nil {
		t.Fatal(err)
	}
	if verifyTrellisInstall(root) == nil {
		t.Fatal("modified binary accepted")
	}
}

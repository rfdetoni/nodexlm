package llamaruntime

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestRuntimeRevisionContract(t *testing.T) {
	if len(SourceCommit) != 40 || len(Version) < 2 || !strings.HasPrefix(Version, "b") {
		t.Fatal("invalid runtime revision")
	}
	if _, err := hex.DecodeString(SourceCommit); err != nil {
		t.Fatal(err)
	}
	for target, digest := range revision.Published {
		if len(digest) != 64 {
			t.Fatalf("invalid hash for %s", target)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			t.Fatal(err)
		}
	}
	for target, bundle := range releases {
		if !strings.Contains(bundle.Name, Version) {
			t.Errorf("runtime asset version drift: %s", target)
		}
	}
	for _, path := range []string{"../../android/app/build.gradle.kts", "../../android/app/src/main/cpp/CMakeLists.txt", "../../.github/workflows/rpc-runtime.yml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "internal/llamaruntime/revision.json") {
			t.Errorf("%s does not read canonical revision", path)
		}
	}
}

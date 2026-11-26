package safedownload

import (
	"strings"
	"testing"
)

func TestXetLocationRejectsUntrustedAndMutableSources(t *testing.T) {
	rev := strings.Repeat("a", 40)
	cases := []string{
		"http://huggingface.co/o/r/resolve/" + rev + "/model.gguf",
		"https://huggingface.co.evil.example/o/r/resolve/" + rev + "/model.gguf",
		"https://huggingface.co/o/r/resolve/main/model.gguf",
		"https://huggingface.co/o/r/resolve/" + rev + "/%2E%2E/model.gguf",
		"https://huggingface.co/o/r/resolve/" + rev + "/model.gguf?x=1",
		"https://huggingface.co/o/r/resolve/" + rev + "/weights.bin",
	}
	for _, input := range cases {
		if _, err := parseXetLocation(input); err == nil {
			t.Errorf("unsafe source accepted: %s", input)
		}
	}
}

func TestXetLocationPinsNestedGGUF(t *testing.T) {
	rev := strings.Repeat("a", 40)
	parsed, err := parseXetLocation("https://huggingface.co/owner/repo/resolve/" + rev + "/models/weights.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Repository != "owner/repo" || parsed.Filename != "models/weights.gguf" || parsed.Revision != rev {
		t.Fatalf("unexpected location: %+v", parsed)
	}
}

package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"
)

func TestVisionContent(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	raw := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	if !validInlineImage(raw) {
		t.Fatal("valid PNG rejected")
	}
	for _, url := range []string{"http://127.0.0.1/private", "file:///etc/passwd", "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b.Bytes())} {
		if validInlineImage(url) {
			t.Fatal("unsafe or mismatched image accepted")
		}
	}
	content, err := json.Marshal([]any{map[string]any{"type": "image_url", "image_url": map[string]string{"url": raw}}})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	if _, err := visionContent(content, &count); err != nil {
		t.Fatal(err)
	}
	if _, err := visionContent(content, &count); err == nil {
		t.Fatal("accepted multiple images")
	}
	malicious, err := json.Marshal([]any{map[string]any{
		"type": "image_url", "image_url": map[string]string{"url": "http://127.0.0.1/private", "URL": raw},
	}})
	if err != nil {
		t.Fatal(err)
	}
	count = 0
	if _, err := visionContent(malicious, &count); err == nil {
		t.Fatal("case-insensitive URL substitution bypassed validation")
	}
	if err := textContent(content); err == nil {
		t.Fatal("text-only runtime accepted image")
	}
}

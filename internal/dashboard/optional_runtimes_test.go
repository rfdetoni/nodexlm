package dashboard

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOptionalRuntimeAuthorizationAndDisabledDefaults(t *testing.T) {
	t.Setenv("NODEXLM_SD_MODEL", "")
	t.Setenv("NODEXLM_FALLBACK_URL", "")
	h, m := newTestUI(t)
	defer m.Close()
	for _, path := range []string{"/api/images/generations", "/v1/images/generations", "/api/chat/fallback"} {
		if got := req(h, http.MethodPost, path, "", "", map[string]string{}).Code; got != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated: %d", path, got)
		}
		if got := req(h, http.MethodPost, path, strings.Repeat("a", 64), "", map[string]string{}).Code; got != http.StatusServiceUnavailable {
			t.Fatalf("%s disabled: %d", path, got)
		}
	}
}

func TestDashboardDOCXExtraction(t *testing.T) {
	h, m := newTestUI(t)
	defer m.Close()
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	w, err := z.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello &amp; world</w:t></w:r></w:p></w:body></w:document>`)); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://localhost/api/documents/text?kind=docx", &archive)
	r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 64))
	r.Header.Set("Content-Type", "application/octet-stream")
	result := httptest.NewRecorder()
	h.ServeHTTP(result, r)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "Hello \\u0026 world") && !strings.Contains(result.Body.String(), "Hello & world") {
		t.Fatalf("unexpected response %d: %s", result.Code, result.Body.String())
	}
}

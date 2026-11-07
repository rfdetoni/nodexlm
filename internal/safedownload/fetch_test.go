package safedownload

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResumeAfterInterruptedDownload(t *testing.T) {
	payload := "GGUF" + strings.Repeat("weights", 1024)
	hash := sha256.Sum256([]byte(payload))
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, hash))
		if requests == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.WriteHeader(200)
			_, _ = w.Write([]byte(payload[:100]))
			return
		}
		if r.Header.Get("Range") != "bytes=100-" || r.Header.Get("If-Range") != fmt.Sprintf(`"%x"`, hash) {
			t.Errorf("missing validated resume: %v", r.Header)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 100-%d/%d", len(payload)-1, len(payload)))
		w.WriteHeader(206)
		_, _ = w.Write([]byte(payload[100:]))
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "model.part")
	if _, err := Fetch(context.Background(), server.Client(), server.URL, target, 1<<20, nil); err == nil {
		t.Fatal("incomplete transfer accepted")
	}
	if n, err := Fetch(context.Background(), server.Client(), server.URL, target, 1<<20, nil); err != nil || n != int64(len(payload)) {
		t.Fatal(n, err)
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != payload {
		t.Fatal("resumed bytes differ")
	}
}

func TestResumeRejectsWrongRangeAndHash(t *testing.T) {
	for _, tc := range []string{"wrong-range", "bad-hash", "ignored-range"} {
		t.Run(tc, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "partial")
			_ = os.WriteFile(target, []byte("GGUFold"), 0600)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc == "wrong-range" {
					w.Header().Set("Content-Range", "bytes 0-7/8")
					w.WriteHeader(206)
					_, _ = w.Write([]byte("GGUFdata"))
					return
				}
				if tc == "bad-hash" {
					w.Header().Set("ETag", `"`+strings.Repeat("0", 64)+`"`)
				}
				_, _ = w.Write([]byte("GGUFfresh"))
			}))
			defer server.Close()
			_ = os.WriteFile(target+".json", []byte(fmt.Sprintf(`{"source":%q,"validator":"v1","total":100}`, server.URL)), 0600)
			_, err := Fetch(context.Background(), server.Client(), server.URL, target, 1<<20, nil)
			if tc == "ignored-range" {
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := os.ReadFile(target)
				if string(raw) != "GGUFfresh" {
					t.Fatal("Range ignored but old prefix retained")
				}
			} else if err == nil {
				t.Fatal("corrupt download accepted")
			}
		})
	}
}
func TestLFSRedirectDigestIsVerified(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/resolve" {
			w.Header().Set("X-Linked-Etag", strings.Repeat("0", 64))
			http.Redirect(w, r, "/cdn", 302)
			return
		}
		_, _ = w.Write([]byte("GGUFbad"))
	}))
	defer s.Close()
	if _, err := Fetch(context.Background(), s.Client(), s.URL+"/resolve", filepath.Join(t.TempDir(), "p"), 100, nil); err == nil {
		t.Fatal("lost LFS redirect checksum")
	}
}

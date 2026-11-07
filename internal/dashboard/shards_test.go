// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dashboard

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type modelTestTransport struct{ endpoint string }

func (t modelTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = "http"
	cloned.URL.Host = strings.TrimPrefix(t.endpoint, "http://")
	return http.DefaultTransport.RoundTrip(cloned)
}

func TestDashboardDownloadsCompleteGGUFShards(t *testing.T) {
	testCases := []struct {
		name string
		failSecond bool
	}{
		{name: "complete"},
		{name: "incomplete", failSecond: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gguf := make([]byte, 24) // GGUF v3 + zero tensor/metadata count
			copy(gguf, "GGUF")
			binary.LittleEndian.PutUint32(gguf[4:8], 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/owner/repo/resolve/main/test-00001-of-00002.gguf":
					_, _ = w.Write(gguf)
				case "/owner/repo/resolve/main/test-00002-of-00002.gguf":
					if tc.failSecond {
						http.Error(w, "not found", http.StatusNotFound)
						return
					}
					_, _ = w.Write(gguf)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			base := t.TempDir()
			models := filepath.Join(base, "models")
			m, err := NewManager(base, models, "")
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			m.httpClient = &http.Client{Transport: modelTestTransport{endpoint: server.URL}}
			err = m.StartDownload("https://huggingface.co/owner/repo/resolve/main/test-00001-of-00002.gguf", "test-00001-of-00002.gguf")
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for state := m.DownloadState(); state != nil && state.Status == "downloading"; state = m.DownloadState() {
				if time.Now().After(deadline) {
					t.Fatal("download did not complete")
				}
				time.Sleep(10 * time.Millisecond)
			}
			state := m.DownloadState()
			expected := "completed"
			if tc.failSecond {
				expected = "failed"
			}
			if state == nil || state.Status != expected {
				t.Fatalf("unexpected download state: %+v", state)
			}
			for _, name := range []string{"test-00001-of-00002.gguf", "test-00002-of-00002.gguf"} {
				_, err = os.Stat(filepath.Join(models, name))
				if tc.failSecond && !os.IsNotExist(err) {
					t.Fatalf("partial model file %s published after error: %v", name, err)
				}
				if !tc.failSecond && err != nil {
					t.Fatalf("complete model missing shard %s: %v", name, err)
				}
			}
		})
	}
}

func TestShardDownloadRejectsCustomName(t *testing.T) {
	m, err := NewManager(t.TempDir(), filepath.Join(t.TempDir(), "models"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.StartDownload("https://huggingface.co/o/r/resolve/main/first-00001-of-00002.gguf", "changed.gguf"); err == nil {
		t.Fatal("custom first shard name would break llama.cpp automatic part discovery")
	}
}


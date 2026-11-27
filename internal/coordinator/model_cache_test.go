package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rfdetoni/nodexlm/internal/safedownload"
)

func TestAuthenticatedModelCacheRejectsUnauthenticatedAndStaleContent(t *testing.T) {
	t.Setenv("NODEXLM_MODEL_P2P", "1")
	root := t.TempDir()
	body := []byte("GGUF verified cache payload")
	digest := sha256.Sum256(body)
	sha := hex.EncodeToString(digest[:])
	file := filepath.Join(root, "weights.gguf")
	if err := os.WriteFile(file, body, 0600); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, ".metadata")
	metadata := fmt.Sprintf("{\"sha256\":%q,\"total\":%d}", sha, len(body))
	if err := os.WriteFile(stage+".json", []byte(metadata), 0600); err != nil {
		t.Fatal(err)
	}
	safedownload.RememberPublished(safedownload.WithContentCache(t.Context(), root), stage, file)

	server, err := New(strings.Repeat("a", 32), strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SetContentCacheDirectory(root); err != nil {
		t.Fatal(err)
	}
	token := "trusted-node-token"
	server.nodes["node-1"] = &nodeState{credential: sha256.Sum256([]byte(token))}
	path := "/v1/nodes/cache/" + sha
	request := func(authorized bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if authorized {
			req.Header.Set("X-Nodex-Node", "node-1")
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		server.LANHandler().ServeHTTP(recorder, req)
		return recorder
	}
	if got := request(false).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized cache: %d", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if file, err := safedownload.OpenVerifiedContent(ctx, root, sha); err == nil {
		file.Close()
		t.Fatal("cancelled cache verification must not read model bytes")
	}
	for range cap(server.modelCacheSlots) {
		server.modelCacheSlots <- struct{}{}
	}
	if got := request(true).Code; got != http.StatusTooManyRequests {
		t.Fatalf("concurrent model cache request accepted: %d", got)
	}
	for range cap(server.modelCacheSlots) {
		<-server.modelCacheSlots
	}
	ok := request(true)
	if ok.Code != 200 || ok.Body.String() != string(body) || ok.Header().Get("ETag") != fmt.Sprintf("%q", sha) {
		t.Fatalf("authenticated cache invalid: status=%d body=%q", ok.Code, ok.Body.String())
	}
	// HEAD authenticates and validates the local immutable cache before the
	// mobile client launches at most four concurrent byte-range GET streams.
	head := httptest.NewRequest(http.MethodHead, path, nil)
	head.Header.Set("X-Nodex-Node", "node-1")
	head.Header.Set("Authorization", "Bearer "+token)
	preflight := httptest.NewRecorder()
	server.LANHandler().ServeHTTP(preflight, head)
	if preflight.Code != http.StatusOK || preflight.Body.Len() != 0 ||
		preflight.Header().Get("ETag") != fmt.Sprintf("%q", sha) {
		t.Fatalf("authenticated HEAD preflight failed: status=%d body=%q", preflight.Code, preflight.Body.String())
	}
	// ServeContent must honor If-Range and return only the requested bytes.
	partial := httptest.NewRequest(http.MethodGet, path, nil)
	partial.Header.Set("X-Nodex-Node", "node-1")
	partial.Header.Set("Authorization", "Bearer "+token)
	partial.Header.Set("Range", "bytes=0-3")
	partial.Header.Set("If-Range", fmt.Sprintf("%q", sha))
	chunk := httptest.NewRecorder()
	server.LANHandler().ServeHTTP(chunk, partial)
	if chunk.Code != http.StatusPartialContent || chunk.Body.String() != "GGUF" ||
		chunk.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-3/%d", len(body)) ||
		chunk.Header().Get("ETag") != fmt.Sprintf("%q", sha) {
		t.Fatalf("authenticated byte range invalid: status=%d headers=%v body=%q", chunk.Code, chunk.Header(), chunk.Body.String())
	}
	// A fifth concurrent transfer must be refused; four bounded streams are
	// admitted so Android can overlap actual Host-to-Guest weight reads.
	if got := cap(server.modelCacheSlots); got != 4 {
		t.Fatalf("expected bounded four-stream cache admission, got %d", got)
	}
	if err := os.WriteFile(file, []byte("GGUF tampered content"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := request(true).Code; got != http.StatusNotFound {
		t.Fatalf("stale model accepted: %d", got)
	}
}

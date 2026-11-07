// Package requestlog adds correlation without wrapping ResponseWriter; raw
// CONNECT must retain Hijacker and SSE must retain Flusher.
package requestlog

import (
	"context"
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"
)

type requestKey struct{}

func Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(requestKey{}) != nil {
			next.ServeHTTP(w, r)
			return
		}
		id := rand.Text()
		r = r.WithContext(context.WithValue(r.Context(), requestKey{}, id))
		w.Header().Set("X-Request-ID", id)
		start := time.Now()
		defer func() {
			slog.Debug("HTTP request", "request_id", id, "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(w, r)
	})
}

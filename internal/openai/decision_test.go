package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDecisionPassThroughAndValidation(t *testing.T) {
	var calls int
	h := &Handler{Decision: func(_ context.Context, model, path string, body []byte, receive func(int, http.Header, io.Reader) error) error {
		calls++
		if model != "decider.gguf" || path != "/v1/systemone" {
			t.Fatalf("unexpected routing: %s %s", model, path)
		}
		if !strings.Contains(string(body), "\"questions\"") {
			t.Fatalf("typed questions lost: %s", body)
		}
		return receive(200, http.Header{"X-Typesafe-Request-Id": []string{"upstream-id"}}, strings.NewReader(`{"answers":{"urgent":{"noul":0.98}}}`))
	}}
	valid := `{"model":"decider.gguf","state":{"message":"Refund please"},"questions":{"urgent":{"type":"noul","instructions":"Is this urgent?"},"team":{"type":"choice","instructions":"Which team?","criteria":{"sales":null,"billing":null}},"rating":{"type":"score","instructions":"How upset?","criteria":["calm","upset","angry"]}}}`
	for _, path := range []string{"/v1/systemone", "/v1/decisions"} {
		if !IsPath(path) {
			t.Fatalf("missing inference route %s", path)
		}
		w := request(h, "POST", path, "a", valid)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"noul":0.98`) {
			t.Fatalf("status=%d response=%s", w.Code, w.Body.String())
		}
		if w.Header().Get("x-typesafe-request-id") != "upstream-id" {
			t.Fatal("upstream request id lost")
		}
	}
	for _, tc := range []struct {
		method, body string
		status       int
	}{
		{"GET", valid, 405},
		{"POST", `{"model":"x","state":"ok","questions":{}}`, 400},
		{"POST", `{"model":"x","state":"ok","questions":{"q":{"type":"other","instructions":"x"}}}`, 400},
		{"POST", `{"model":"x","state":42,"questions":{"q":{"type":"noul","instructions":"x"}}}`, 400},
		{"POST", `{"model":"x","state":"ok","questions":{"q":{"type":"choice","instructions":"x","criteria":{"only":null}}}}`, 400},
		{"POST", `{"model":"x","state":"ok","questions":{"q":{"type":"score","instructions":"x","criteria":["one"]}}}`, 400},
		{"POST", `{"model":"x","state":"ok","questions":{"q":{"type":"noul","instructions":true}}}`, 400},
		{"POST", `{"state":"ok","questions":{"q":{"type":"noul","instructions":"x"}}}`, 400},
	} {
		w := request(h, tc.method, "/v1/systemone", "a", tc.body)
		if w.Code != tc.status {
			t.Errorf("%s %s: got %d %s, expected %d", tc.method, tc.body, w.Code, w.Body, tc.status)
		}
	}
	if calls != 2 {
		t.Fatalf("invalid requests forwarded: %d", calls)
	}
}

func TestDecisionUpstreamErrorsRemainTyped(t *testing.T) {
	h := &Handler{Decision: func(_ context.Context, _, _ string, _ []byte, receive func(int, http.Header, io.Reader) error) error {
		return receive(422, http.Header{}, strings.NewReader(`{"error":{"code":"STATE_TRUNCATED","message":"state too long"}}`))
	}}
	w := request(h, "POST", "/v1/systemone", "a", `{"model":"m","state":"ok","questions":{"q":{"type":"noul","instructions":"x"}}}`)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "STATE_TRUNCATED") {
		t.Fatalf("upstream error contract lost: %d %s", w.Code, w.Body)
	}
}

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecisionRuntimeOnlyAcceptsLoopback(t *testing.T) {
	for _, origin := range []string{
		"https://127.0.0.1:11435", "http://localhost:11435",
		"http://example.com", "http://169.254.169.254", "http://127.0.0.1:11435/path",
		"http://user:pass@127.0.0.1:11435",
	} {
		if _, err := NewLocalDecisionEngine(origin); err == nil {
			t.Errorf("unsafe decision runtime origin accepted: %s", origin)
		}
	}
}

func TestDecisionEngineProbesModelAndPreservesTypedJSON(t *testing.T) {
	var called int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"models":[{"name":"laya:multilingual"}]}`)
		case "/v1/systemone":
			called++
			if r.Header.Get("Authorization") != "" {
				t.Fatal("client credential unexpectedly forwarded to local runtime")
			}
			var input map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if string(input["model"]) != `"laya:multilingual"` || !strings.Contains(string(input["questions"]), "urgent") {
				t.Fatalf("lost typed payload: %+v", input)
			}
			fmt.Fprint(w, `{"answers":{"urgent":{"noul":0.83}}}`)
		default:
			http.NotFound(w,r)
		}
	}))
	defer upstream.Close()
	engine, err := NewLocalDecisionEngine(upstream.URL)
	if err != nil { t.Fatal(err) }
	if err := engine.Check(context.Background(),"ollaya/laya:multilingual"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Check(context.Background(),"unknown"); err == nil { t.Fatal("unavailable model advertised") }
	response, err := engine.Decide(context.Background(),"ollaya/laya:multilingual",
		json.RawMessage(`{"state":"Urgent","questions":{"urgent":{"type":"noul","instructions":"urgent?"}}}`))
	if err != nil || string(response) != `{"answers":{"urgent":{"noul":0.83}}}` || called != 1 {
		t.Fatalf("incorrect typed response %s, %v, calls=%d", response,err,called)
	}
}

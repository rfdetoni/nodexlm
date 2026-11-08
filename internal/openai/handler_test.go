package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func request(h http.Handler, method, path, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAgentPayloadAndNativeErrors(t *testing.T) {
	var received map[string]any
	h := &Handler{Forward: func(_ context.Context, model, path string, body []byte, receive func(int, http.Header, io.Reader) error) error {
		if model != "m" || path != "/v1/chat/completions" {
			t.Fatal(model, path)
		}
		_ = json.Unmarshal(body, &received)
		return receive(400, http.Header{}, strings.NewReader(`{"error":{"message":"context exceeded","type":"invalid_request_error","code":"context_length_exceeded"}}`))
	}}
	payload := `{"model":"m","max_completion_tokens":10000,"temperature":0,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],"tool_choice":"required","parallel_tool_calls":true,"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"}}},"messages":[{"role":"developer","content":"Use tools"},{"role":"user","content":[{"type":"text","text":"Find weather"}]},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"20C"}]}`
	w := request(h, "POST", "/v1/chat/completions", "a", payload)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "context_length_exceeded") || !strings.Contains(w.Body.String(), `"param":null`) {
		t.Fatal(w.Code, w.Body)
	}
	if received["max_tokens"] != float64(10000) || received["temperature"] != float64(0) || received["tools"] == nil || received["response_format"] == nil {
		t.Fatal(received)
	}
	messages := received["messages"].([]any)
	if messages[2].(map[string]any)["tool_calls"] == nil || messages[3].(map[string]any)["tool_call_id"] != "call_1" {
		t.Fatal(messages)
	}
}
func TestResponsesContinuationStorageAndCredentialIsolation(t *testing.T) {
	var calls []map[string]json.RawMessage
	h := &Handler{Forward: func(_ context.Context, _, _ string, body []byte, receive func(int, http.Header, io.Reader) error) error {
		var in map[string]json.RawMessage
		_ = json.Unmarshal(body, &in)
		calls = append(calls, in)
		return receive(200, http.Header{}, strings.NewReader(`{"id":"resp_1","object":"response","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}]}`))
	}}
	if w := request(h, "POST", "/v1/responses", "a", `{"model":"m","instructions":"first only","input":"weather?"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(h, "GET", "/v1/responses/resp_1", "b", ""); w.Code != 404 {
		t.Fatal("credential leaked", w.Code)
	}
	w := request(h, "POST", "/v1/responses", "a", `{"model":"m","previous_response_id":"resp_1","instructions":"new instructions","input":[{"type":"function_call_output","call_id":"call_1","output":"20C"}]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var items []map[string]any
	_ = json.Unmarshal(calls[1]["input"], &items)
	if len(items) != 3 || items[1]["call_id"] != "call_1" || items[2]["output"] != "20C" {
		t.Fatal(items)
	}
	if string(calls[1]["instructions"]) != `"new instructions"` || calls[1]["previous_response_id"] != nil {
		t.Fatal(calls[1])
	}
	if w := request(h, "GET", "/v1/responses/resp_1/input_items", "a", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(h, "DELETE", "/v1/responses/resp_1", "a", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(h, "GET", "/v1/responses/resp_1", "a", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/v1/responses", "a", `{"model":"m","store":false,"input":"private"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(h, "GET", "/v1/responses/resp_1", "a", ""); w.Code != 404 {
		t.Fatal("store:false retained data")
	}
}
func TestNamedStreamingEventsAndTruncation(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "truncated"}[broken], func(t *testing.T) {
			events := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_s\",\"output\":null}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"olá\"}\n\n"
			if !broken {
				events += "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s\",\"output\":[]}}\n\n"
			}
			h := &Handler{Forward: func(_ context.Context, _, _ string, _ []byte, receive func(int, http.Header, io.Reader) error) error {
				return receive(200, http.Header{"Content-Type": []string{"text/event-stream"}}, strings.NewReader(events))
			}}
			w := request(h, "POST", "/v1/responses", "a", `{"model":"m","input":"hello","stream":true}`)
			if !strings.Contains(w.Body.String(), `"output":[]`) {
				t.Fatal("SDK stream snapshot must have an output array", w.Body)
			}
			if !strings.Contains(w.Body.String(), "event: response.output_text.delta") || !strings.Contains(w.Body.String(), "olá") {
				t.Fatal(w.Body)
			}
			if broken {
				if !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "response.completed") {
					t.Fatal(w.Body)
				}
			} else if w := request(h, "GET", "/v1/responses/resp_s", "a", ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
}
func TestUnsupportedModalitiesAndHostedToolsFailExplicitly(t *testing.T) {
	h := &Handler{Forward: func(context.Context, string, string, []byte, func(int, http.Header, io.Reader) error) error {
		t.Fatal("unsupported request reached runtime")
		return nil
	}}
	for _, body := range []string{`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"http://127.0.0.1/private"}}]}]}`, `{"model":"m","input":"x","tools":[{"type":"web_search"}]}`, `{"model":"m","input":"x","background":true}`, `{"model":"m","max_completion_tokens":5,"max_tokens":4,"input":"x"}`} {
		w := request(h, "POST", "/v1/chat/completions", "a", body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"type":"invalid_request_error"`) {
			t.Fatal(w.Code, w.Body)
		}
	}
}
func TestCancellationReachesRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := &Handler{Forward: func(ctx context.Context, _, _ string, _ []byte, _ func(int, http.Header, io.Reader) error) error {
		if ctx.Err() != context.Canceled {
			t.Fatal("lost cancellation")
		}
		return ctx.Err()
	}}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"m","input":"x"}`)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), r)
}

func TestInvalidNativeResponseCannotPanicOrSucceed(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"id":"resp_x","output":{}}`} {
		h := &Handler{Forward: func(_ context.Context, _, _ string, _ []byte, receive func(int, http.Header, io.Reader) error) error {
			return receive(200, http.Header{}, strings.NewReader(raw))
		}}
		w := request(h, "POST", "/v1/responses", "a", `{"model":"m","store":false,"input":"x"}`)
		if w.Code != 502 || !strings.Contains(w.Body.String(), `"type":"server_error"`) {
			t.Fatal(raw, w.Code, w.Body)
		}
	}
}

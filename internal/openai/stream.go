package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func relayStream(ctx context.Context, w http.ResponseWriter, reader io.Reader, path string, legacy bool, completed func([]byte), observe func([]byte)) (result error) {
	sequence := 0
	defer func() {
		if result != nil && path == "/v1/responses" {
			result = &streamFailure{cause: result, sequence: sequence}
		}
	}()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var lines []string
	size := 0
	terminal := false
	send := func() error {
		if len(lines) == 0 {
			return nil
		}
		var data []string
		event := ""
		for _, line := range lines {
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
			if strings.HasPrefix(line, "event:") {
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
		}
		payload := strings.Join(data, "\n")
		if payload != "" && payload != "[DONE]" {
			if legacy {
				payload = string(legacyChatResponse([]byte(payload), true))
				rebuilt := []string{}
				for _, line := range lines {
					if !strings.HasPrefix(line, "data:") {
						rebuilt = append(rebuilt, line)
					}
				}
				lines = append(rebuilt, "data: "+payload)
			}
			var item map[string]json.RawMessage
			if json.Unmarshal([]byte(payload), &item) != nil {
				return errors.New("Runtime returned an invalid SSE event")
			}

			if nested := item["response"]; path == "/v1/responses" && len(nested) > 0 {
				normalized := normalizeResponse(nested)
				item["response"] = normalized
				encoded, _ := json.Marshal(item)
				payload = string(encoded)
				rebuilt := []string{}
				for _, line := range lines {
					if !strings.HasPrefix(line, "data:") {
						rebuilt = append(rebuilt, line)
					}
				}
				lines = append(rebuilt, "data: "+payload)
			}
			var current int
			if json.Unmarshal(item["sequence_number"], &current) == nil && current >= sequence {
				sequence = current + 1
			}
			kind, _ := stringField(item, "type")
			if nested := item["response"]; len(nested) > 0 {
				observe(nested)
			} else {
				observe([]byte(payload))
			}
			if event == "" {
				event = kind
			}
			if path == "/v1/responses" && (event == "response.completed" || event == "response.incomplete" || event == "response.failed") {
				terminal = true
				if event != "response.failed" {
					completed(item["response"])
				}
			}
		}
		if payload == "[DONE]" {
			terminal = true
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(15 * time.Second))
		if _, err := io.WriteString(w, strings.Join(lines, "\n")+"\n\n"); err != nil {
			return err
		}
		lines = nil
		size = 0
		return http.NewResponseController(w).Flush()
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		size += len(line) + 1
		if size > 1<<20 {
			return errors.New("Runtime SSE event exceeded the size limit")
		}
		if line == "" {
			if err := send(); err != nil {
				return err
			}
			if terminal {
				return nil
			}
		} else {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(lines) > 0 {
		if err := send(); err != nil {
			return err
		}
	}
	if !terminal {
		return errors.New("Runtime stream ended before its terminal event")
	}
	return nil
}

type streamFailure struct {
	cause    error
	sequence int
}

func (e *streamFailure) Error() string { return e.cause.Error() }
func (e *streamFailure) Unwrap() error { return e.cause }
func streamError(w http.ResponseWriter, path string, err error) error {
	var raw []byte
	var e error
	if path == "/v1/responses" {
		sequence := 0
		var failure *streamFailure
		if errors.As(err, &failure) {
			sequence = failure.sequence
		}
		raw, _ = json.Marshal(map[string]any{"type": "error", "message": err.Error(), "code": "inference_error", "param": nil, "sequence_number": sequence})
		_, e = fmt.Fprintf(w, "event: error\ndata: %s\n\n", raw)
	} else {
		raw, _ = json.Marshal(map[string]any{"error": map[string]any{"message": err.Error(), "type": "server_error", "param": nil, "code": "inference_error"}})
		_, e = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	_ = http.NewResponseController(w).Flush()
	return e
}

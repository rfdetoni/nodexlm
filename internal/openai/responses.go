package openai

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) prepareResponse(in map[string]json.RawMessage, owner string) ([]json.RawMessage, bool, error) {
	keep := true
	if raw := in["store"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &keep) != nil {
			return nil, false, invalid("store", "store must be a boolean")
		}
	}
	input := []json.RawMessage{}
	var text string
	if len(in["input"]) == 0 || string(in["input"]) == "null" {
		// An input-less continuation may use previous_response_id/instructions.
	} else if json.Unmarshal(in["input"], &text) == nil {
		item, _ := json.Marshal(map[string]any{"role": "user", "content": text})
		input = []json.RawMessage{item}
	} else if json.Unmarshal(in["input"], &input) != nil {
		return nil, false, invalid("input", "input must be text or an array of input items")
	}
	for index, raw := range input {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil || item == nil {
			return nil, false, invalid("input", "Each input item must be an object")
		}
		if err := textContent(item["content"]); err != nil {
			return nil, false, err
		}
		kind, _ := stringField(item, "type")
		if len(item["id"]) == 0 || string(item["id"]) == "null" {
			id := make([]byte, 16)
			if _, err := rand.Read(id); err != nil {
				return nil, false, err
			}
			item["id"], _ = json.Marshal("item_" + hex.EncodeToString(id))
			input[index], _ = json.Marshal(item)
		}
		if kind == "function_call_output" {
			if err := textContent(item["output"]); err != nil {
				return nil, false, err
			}
		}
	}
	previous, err := stringField(in, "previous_response_id")
	if err != nil {
		return nil, false, invalid("previous_response_id", "previous_response_id must be a string")
	}
	if previous != "" {
		saved, ok := h.store.get(previous, owner)
		if !ok {
			return nil, false, &APIError{Status: 404, Message: "Previous response not found or expired", Param: "previous_response_id", Code: "response_not_found"}
		}
		history := append([]json.RawMessage{}, saved.Input...)
		history = append(history, saved.Output...)
		input = append(history, input...)
	}
	delete(in, "previous_response_id")
	in["input"], _ = json.Marshal(input)
	in["store"] = json.RawMessage("false")
	return input, keep, nil
}
func (h *Handler) remember(body []byte, input []json.RawMessage, owner string) bool {
	var response struct {
		ID     string            `json:"id"`
		Output []json.RawMessage `json:"output"`
	}
	if json.Unmarshal(body, &response) != nil || response.ID == "" || response.Output == nil {
		return false
	}
	return h.store.put(response.ID, storedResponse{Owner: owner, Created: time.Now(), Body: append(json.RawMessage{}, body...), Input: append([]json.RawMessage{}, input...), Output: response.Output})
}
func (h *Handler) savedResponse(w http.ResponseWriter, r *http.Request, owner string) {
	suffix := strings.TrimPrefix(r.URL.Path, "/v1/responses/")
	id, tail, _ := strings.Cut(suffix, "/")
	if r.Method == "DELETE" && tail == "" {
		if !h.store.remove(id, owner) {
			Error(w, 404, "Response not found", "id", "response_not_found")
			return
		}
		JSON(w, 200, map[string]any{"id": id, "object": "response", "deleted": true})
		return
	}
	if r.Method != "GET" {
		Error(w, 405, "Stored responses support GET and DELETE", "", "method_not_allowed")
		return
	}
	saved, ok := h.store.get(id, owner)
	if !ok {
		Error(w, 404, "Response not found or expired", "id", "response_not_found")
		return
	}
	if tail == "" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(saved.Body)
		return
	}
	if tail == "input_items" {
		items := append([]json.RawMessage{}, saved.Input...)
		order := r.URL.Query().Get("order")
		if order == "" {
			order = "desc"
		}
		if order != "asc" && order != "desc" {
			Error(w, 400, "order must be asc or desc", "order", "invalid_parameter")
			return
		}
		if order == "desc" {
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
		}
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 1 || n > 100 {
				Error(w, 400, "limit must be between 1 and 100", "limit", "invalid_parameter")
				return
			}
			limit = n
		}
		itemID := func(raw json.RawMessage) string {
			var item map[string]json.RawMessage
			_ = json.Unmarshal(raw, &item)
			id, _ := stringField(item, "id")
			return id
		}
		for _, cursor := range []string{"after", "before"} {
			if wanted := r.URL.Query().Get(cursor); wanted != "" {
				found := -1
				for i, item := range items {
					if itemID(item) == wanted {
						found = i
						break
					}
				}
				if found < 0 {
					Error(w, 400, "Unknown pagination cursor", cursor, "invalid_parameter")
					return
				}
				if cursor == "after" {
					items = items[found+1:]
				} else {
					items = items[:found]
				}
			}
		}
		more := len(items) > limit
		if more {
			items = items[:limit]
		}
		var first, last any
		if len(items) > 0 {
			first = itemID(items[0])
			last = itemID(items[len(items)-1])
		}
		JSON(w, 200, map[string]any{"object": "list", "data": items, "has_more": more, "first_id": first, "last_id": last})
		return
	}
	Error(w, 404, "Unknown response endpoint", "", "not_found")
}

// Some native versions send output:null in response.created. SDK streaming
// accumulators require an empty list before receiving output_item.added.
func normalizeResponse(body []byte) ([]byte, error) {
	var response map[string]json.RawMessage
	if json.Unmarshal(body, &response) != nil || response == nil {
		return nil, errors.New("Runtime returned an invalid response snapshot")
	}
	id, e := stringField(response, "id")
	if e != nil || id == "" {
		return nil, errors.New("Runtime response is missing its ID")
	}
	if len(response["output"]) == 0 || string(response["output"]) == "null" {
		response["output"] = json.RawMessage("[]")
	} else {
		var output []json.RawMessage
		if json.Unmarshal(response["output"], &output) != nil {
			return nil, errors.New("Runtime response output must be an array")
		}
	}
	return json.Marshal(response)
}

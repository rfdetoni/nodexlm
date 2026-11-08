package openai

import (
	"encoding/json"
	"fmt"
)

// Older chat clients still use functions/function_call and role:function.
// Translate both directions; accepting only the input would break their loop.
func normalizeLegacy(in map[string]json.RawMessage, messages []map[string]json.RawMessage) error {
	legacy := false
	if raw := in["functions"]; len(raw) > 0 && string(raw) != "null" {
		if len(in["tools"]) > 0 && string(in["tools"]) != "null" {
			return invalid("functions", "Use functions or tools, not both")
		}
		var functions []json.RawMessage
		if json.Unmarshal(raw, &functions) != nil {
			return invalid("functions", "functions must be an array")
		}
		tools := []any{}
		for _, fn := range functions {
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
		in["tools"], _ = json.Marshal(tools)
		delete(in, "functions")
		legacy = true
	}
	if raw := in["function_call"]; len(raw) > 0 && string(raw) != "null" {
		if len(in["tool_choice"]) > 0 && string(in["tool_choice"]) != "null" {
			return invalid("function_call", "Use function_call or tool_choice, not both")
		}
		var function struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &function) == nil && function.Name != "" {
			in["tool_choice"], _ = json.Marshal(map[string]any{"type": "function", "function": function})
		} else {
			in["tool_choice"] = raw
		}
		delete(in, "function_call")
		legacy = true
	}
	for _, message := range messages {
		role, _ := stringField(message, "role")
		if role == "function" || (len(message["function_call"]) > 0 && string(message["function_call"]) != "null") {
			legacy = true
		}
	}
	if !legacy {
		return nil
	}
	in["parallel_tool_calls"] = json.RawMessage("false")
	pending := map[string]string{}
	for index, message := range messages {
		if raw := message["function_call"]; len(raw) > 0 && string(raw) != "null" {
			var function map[string]json.RawMessage
			if json.Unmarshal(raw, &function) != nil {
				return invalid("messages.function_call", "Invalid function call")
			}
			name, _ := stringField(function, "name")
			id := fmt.Sprintf("call_legacy_%d", index)
			pending[name] = id
			message["tool_calls"], _ = json.Marshal([]any{map[string]any{"id": id, "type": "function", "function": function}})
			delete(message, "function_call")
		}
		role, _ := stringField(message, "role")
		if role == "function" {
			name, _ := stringField(message, "name")
			id := pending[name]
			if id == "" {
				return invalid("messages", "Function result has no preceding function call")
			}
			message["role"] = json.RawMessage(`"tool"`)
			message["tool_call_id"], _ = json.Marshal(id)
			delete(message, "name")
		}
	}
	in["messages"], _ = json.Marshal(messages)
	return nil
}
func legacyChatResponse(body []byte, stream bool) []byte {
	var response map[string]json.RawMessage
	if json.Unmarshal(body, &response) != nil {
		return body
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(response["choices"], &choices) != nil {
		return body
	}
	key := "message"
	if stream {
		key = "delta"
	}
	for _, choice := range choices {
		var message map[string]json.RawMessage
		if json.Unmarshal(choice[key], &message) == nil {
			var calls []map[string]json.RawMessage
			if json.Unmarshal(message["tool_calls"], &calls) == nil && len(calls) > 0 {
				if fn := calls[0]["function"]; len(fn) > 0 {
					message["function_call"] = fn
				}
				delete(message, "tool_calls")
				choice[key], _ = json.Marshal(message)
			}
		}
		if string(choice["finish_reason"]) == `"tool_calls"` {
			choice["finish_reason"] = json.RawMessage(`"function_call"`)
		}
	}
	response["choices"], _ = json.Marshal(choices)
	encoded, err := json.Marshal(response)
	if err != nil {
		return body
	}
	return encoded
}

func usesLegacy(in map[string]json.RawMessage) bool {
	for _, key := range []string{"functions", "function_call"} {
		if raw := in[key]; len(raw) > 0 && string(raw) != "null" {
			return true
		}
	}
	var messages []map[string]json.RawMessage
	_ = json.Unmarshal(in["messages"], &messages)
	for _, message := range messages {
		role, _ := stringField(message, "role")
		if role == "function" || (len(message["function_call"]) > 0 && string(message["function_call"]) != "null") {
			return true
		}
	}
	return false
}

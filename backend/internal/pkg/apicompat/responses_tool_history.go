package apicompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// NormalizeResponsesToolHistory fixes identifiable IDs, names and argument
// encodings while preserving history that may be completed by the upstream.
func NormalizeResponsesToolHistory(input json.RawMessage, hasPreviousResponse bool) (json.RawMessage, bool, error) {
	return normalizeResponsesToolHistory(input, hasPreviousResponse, false)
}

// RepairResponsesToolHistory additionally recovers a stateless history rejected
// for missing tool context. It retains interrupted calls and unpaired results
// as explicit notes without inventing results. Remote history stays intact.
func RepairResponsesToolHistory(input json.RawMessage, hasPreviousResponse bool) (json.RawMessage, bool, error) {
	return normalizeResponsesToolHistory(input, hasPreviousResponse, true)
}

func normalizeResponsesToolHistory(input json.RawMessage, hasPreviousResponse, repairIncomplete bool) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || trimmed[0] != '[' || !bytes.Contains(trimmed, []byte(`"function_call`)) {
		return input, false, nil
	}
	var rawItems []json.RawMessage
	if err := json.Unmarshal(input, &rawItems); err != nil {
		return input, false, err
	}
	items := make([]map[string]json.RawMessage, len(rawItems))
	aliases := make(map[string]string)
	usableCalls := make(map[string]bool)
	for index, raw := range rawItems {
		kind := gjson.GetBytes(raw, "type").String()
		if kind == "item_reference" {
			hasPreviousResponse = true
		}
		switch kind {
		case "custom_tool_call", "tool_search_call", "mcp_tool_call", "local_shell_call", "tool_call":
			id := gjson.GetBytes(raw, "call_id").String()
			if id == "" {
				id = gjson.GetBytes(raw, "id").String()
			}
			if id != "" {
				usableCalls[id] = true
			}
		}
		if kind != "function_call" && kind != "function_call_output" {
			continue
		}
		if err := json.Unmarshal(raw, &items[index]); err != nil {
			return input, false, err
		}
		item := items[index]
		if rawString(item["type"]) == "function_call" {
			if id, callID := rawString(item["id"]), rawString(item["call_id"]); id != "" && callID != "" {
				if old, exists := aliases[id]; !exists || old == callID {
					aliases[id] = callID
				} else {
					aliases[id] = "" // Ambiguous references must not be guessed.
				}
			}
		}
	}

	changed := false
	set := func(index int, key, value string) {
		encoded, _ := json.Marshal(value)
		items[index][key] = encoded
		changed = true
	}
	outputs := make(map[string]bool)
	names := make(map[string]string)
	for index, item := range items {
		if rawString(item["type"]) != "function_call_output" {
			continue
		}
		id := rawString(item["call_id"])
		if canonical := aliases[id]; canonical != "" && canonical != id {
			id = canonical
			set(index, "call_id", id)
		}
		if id != "" {
			outputs[id] = true
			if name := rawString(item["name"]); name != "" {
				if old, exists := names[id]; !exists || old == name {
					names[id] = name
				} else {
					names[id] = ""
				}
			}
		}
	}

	for index, item := range items {
		if rawString(item["type"]) != "function_call" {
			continue
		}
		id := rawString(item["call_id"])
		if id == "" {
			id = rawString(item["id"])
			if id != "" && outputs[id] {
				set(index, "call_id", id)
			}
		}
		name := rawString(item["name"])
		if name == "" && names[id] != "" {
			name = names[id]
			set(index, "name", name)
		}
		arguments := bytes.TrimSpace(item["arguments"])
		if len(arguments) > 0 && arguments[0] == '{' {
			set(index, "arguments", string(arguments))
		}
		if repairIncomplete && !hasPreviousResponse && (id == "" || name == "" || !outputs[id]) {
			// A missing output does not mean the tool succeeded. Preserve the
			// attempted call as context, with an explicit interruption marker.
			text := fmt.Sprintf("[Incomplete tool call: name=%q, call_id=%q, arguments=%s. No complete tool-call/result pair was provided.]", name, id, item["arguments"])
			items[index] = toolHistoryNote("assistant", text)
			changed = true
			continue
		}
		if id != "" && name != "" {
			usableCalls[id] = true
		}
	}

	if repairIncomplete && !hasPreviousResponse {
		for index, item := range items {
			if rawString(item["type"]) != "function_call_output" {
				continue
			}
			id := rawString(item["call_id"])
			// Named outputs without call_id are standalone external inputs.
			if usableCalls[id] || (id == "" && strings.TrimSpace(rawString(item["name"])) != "") {
				continue
			}
			output := rawString(item["output"])
			if output == "" {
				output = string(item["output"])
			}
			items[index] = toolHistoryNote("user", fmt.Sprintf("[Unpaired tool result: call_id=%q]\n%s", id, output))
			changed = true
		}
	}
	if !changed {
		return input, false, nil
	}
	for index, item := range items {
		if item == nil {
			continue
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return input, false, err
		}
		rawItems[index] = raw
	}
	result, err := json.Marshal(rawItems)
	return result, true, err
}

func toolHistoryNote(role, text string) map[string]json.RawMessage {
	partType := "input_text"
	if role == "assistant" {
		partType = "output_text"
	}
	content, _ := json.Marshal([]ResponsesContentPart{{Type: partType, Text: text}})
	encodedRole, _ := json.Marshal(role)
	return map[string]json.RawMessage{"type": json.RawMessage(`"message"`), "role": encodedRole, "content": content}
}

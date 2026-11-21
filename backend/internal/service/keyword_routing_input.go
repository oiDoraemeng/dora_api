package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// Extract only literal user text. In particular, never recurse into tool
// results, image metadata, system instructions, or System One evaluated state.
func extractKeywordRoutingText(protocol string, body []byte) (string, bool) {
	root := gjson.ParseBytes(body)
	parts := make([]string, 0, 4)
	tool := false
	add := func(value gjson.Result) {
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			parts = append(parts, value.String())
		}
	}
	collect := func(content gjson.Result) {
		if content.Type == gjson.String {
			add(content)
			return
		}
		content.ForEach(func(_, block gjson.Result) bool {
			switch block.Get("type").String() {
			case "text", "input_text", "":
				add(block.Get("text"))
			case "tool_result", "function_call_output":
				tool = true
			}
			return true
		})
	}
	switch protocol {
	case ContentModerationProtocolOpenAIChat, ContentModerationProtocolAnthropicMessages:
		messages := root.Get("messages").Array()
		for i := len(messages) - 1; i >= 0; i-- {
			last := messages[i]
			switch last.Get("role").String() {
			case "user":
				collect(last.Get("content"))
			case "tool", "function":
				tool = true
			default:
				continue
			}
			break
		}
	case ContentModerationProtocolGemini:
		contents := root.Get("contents").Array()
		for i := len(contents) - 1; i >= 0; i-- {
			last := contents[i]
			if role := last.Get("role").String(); role == "user" || role == "" {
				last.Get("parts").ForEach(func(_, part gjson.Result) bool {
					add(part.Get("text"))
					if part.Get("functionResponse").Exists() {
						tool = true
					}
					return true
				})
				break
			}
		}
	case ContentModerationProtocolTypeSafeSystemOne:
		root.Get("questions").ForEach(func(_, question gjson.Result) bool {
			add(question.Get("instructions"))
			add(question.Get("criteria"))
			return true
		})
	default: // Responses, including the first response.create WebSocket frame.
		input := root.Get("input")
		if input.Type == gjson.String {
			add(input)
		} else {
			items := input.Array()
			for i := len(items) - 1; i >= 0; i-- {
				last := items[i]
				if last.Get("type").String() == "function_call_output" {
					tool = true
					break
				} else if role := last.Get("role").String(); role == "user" || (role == "" && (last.Get("type").String() == "message" || last.Get("type").String() == "")) {
					collect(last.Get("content"))
					break
				} else if last.Get("type").String() == "input_text" {
					add(last.Get("text"))
					break
				}
			}
		}
	}
	return strings.Join(parts, "\n"), tool && len(parts) == 0
}

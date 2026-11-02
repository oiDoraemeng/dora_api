// Package clienterror normalizes client-visible upstream failure messages
// without changing status codes, error types, or provider diagnostics.
package clienterror

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const Message = "stream disconnected before completion: Upstream request failed"

// Rewrite replaces only error-message fields in a JSON protocol payload.
func Rewrite(payload []byte) []byte {
	if !gjson.ValidBytes(payload) {
		return payload
	}
	root := gjson.ParseBytes(payload)
	if root.IsArray() {
		for i, value := range root.Array() {
			original := []byte(value.Raw)
			if updated := Rewrite(original); !bytes.Equal(original, updated) {
				if next, err := sjson.SetRawBytes(payload, strconv.Itoa(i), updated); err == nil {
					payload = next
				}
			}
		}
		return payload
	}
	if !root.IsObject() || !isErrorPayload(root) {
		return payload
	}

	updated := false
	for _, path := range []string{"error.message", "response.error.message", "response.response.error.message"} {
		value := gjson.GetBytes(payload, path)
		if value.Type != gjson.String {
			continue
		}
		if next, err := sjson.SetBytes(payload, path, Message); err == nil {
			payload = next
			updated = true
		}
	}
	if value := gjson.GetBytes(payload, "message"); value.Type == gjson.String {
		if next, err := sjson.SetBytes(payload, "message", Message); err == nil {
			payload = next
			updated = true
		}
	}
	if updated {
		return payload
	}

	// Preserve provider envelopes even when their error object omitted message.
	for _, path := range []string{"error", "response.error", "response.response.error"} {
		value := gjson.GetBytes(payload, path)
		switch value.Type {
		case gjson.String:
			if next, err := sjson.SetBytes(payload, path, Message); err == nil {
				return next
			}
		case gjson.JSON:
			if next, err := sjson.SetBytes(payload, path+".message", Message); err == nil {
				return next
			}
		}
	}
	return payload
}

func isErrorPayload(root gjson.Result) bool {
	if value := root.Get("error"); value.Exists() && value.Type != gjson.Null {
		return true
	}
	if value := root.Get("response.error"); value.Exists() && value.Type != gjson.Null {
		return true
	}
	if value := root.Get("response.response.error"); value.Exists() && value.Type != gjson.Null {
		return true
	}
	switch strings.TrimSpace(root.Get("type").String()) {
	case "error", "response.failed":
		return true
	}
	return strings.EqualFold(strings.TrimSpace(root.Get("status").String()), "failed") ||
		strings.EqualFold(strings.TrimSpace(root.Get("response.status").String()), "failed")
}

// RewriteSSE rewrites JSON data in one complete SSE event.
func RewriteSSE(event []byte) []byte {
	lines := bytes.SplitAfter(event, []byte{'\n'})
	var data [][]byte
	first := -1
	for i, line := range lines {
		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		if first < 0 {
			first = i
		}
		data = append(data, bytes.TrimPrefix(line[len("data:"):], []byte{' '}))
	}
	if first < 0 {
		return event
	}
	payload := bytes.Join(data, []byte{'\n'})
	updated := Rewrite(payload)
	if bytes.Equal(payload, updated) {
		return event
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, updated); err != nil {
		return event
	}
	var out bytes.Buffer
	for i, line := range lines {
		if i == first {
			out.WriteString("data: ")
			out.Write(compact.Bytes())
			if bytes.HasSuffix(line, []byte("\r\n")) {
				out.WriteString("\r\n")
			} else if bytes.HasSuffix(line, []byte{'\n'}) {
				out.WriteByte('\n')
			}
		} else if !bytes.HasPrefix(line, []byte("data:")) {
			out.Write(line)
		}
	}
	return out.Bytes()
}

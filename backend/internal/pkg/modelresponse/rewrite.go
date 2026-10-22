// Package modelresponse restores the public model name at the client boundary.
// Upstream requests and usage accounting continue to use their mapped models.
package modelresponse

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Rewrite changes only existing protocol metadata, never generated content,
// tool arguments, nested image tool models, or error details. It deliberately
// does not compare against the mapped model: providers may return a canonical
// name or snapshot that differs from the model sent upstream.
func Rewrite(payload []byte, requestedModel string) []byte {
	if requestedModel == "" || !gjson.ValidBytes(payload) {
		return payload
	}
	root := gjson.ParseBytes(payload)
	if root.IsArray() {
		// Native Gemini can return a JSON array when streaming without SSE.
		for i, value := range root.Array() {
			original := []byte(value.Raw)
			if updated := Rewrite(original, requestedModel); !bytes.Equal(original, updated) {
				if next, err := sjson.SetRawBytes(payload, strconv.Itoa(i), updated); err == nil {
					payload = next
				}
			}
		}
		return payload
	}
	if !root.IsObject() || root.Get("error").Exists() || root.Get("type").String() == "error" {
		return payload
	}
	for _, path := range []string{"model", "response.model", "message.model", "modelVersion", "response.modelVersion"} {
		value := gjson.GetBytes(payload, path)
		if value.Type != gjson.String || value.String() == requestedModel {
			continue
		}
		if updated, err := sjson.SetBytes(payload, path, requestedModel); err == nil {
			payload = updated
		}
	}
	return payload
}

// RewriteSSE rewrites one complete SSE event, including multi-line data fields.
// Event names, IDs, comments, [DONE], and non-JSON data retain their semantics.
func RewriteSSE(event []byte, requestedModel string) []byte {
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
	updated := Rewrite(payload, requestedModel)
	if bytes.Equal(payload, updated) {
		return event
	}
	// A multi-line JSON document becomes a single data field. Compact only
	// protocol JSON; escaped newlines inside strings are retained.
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

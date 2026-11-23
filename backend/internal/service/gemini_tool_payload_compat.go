package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Decode request objects without converting tool arguments or schema bounds to
// float64. These numbers must survive any compatibility rewrite exactly.
func decodeGeminiRequestObject(body []byte) (map[string]any, error) {
	if !json.Valid(body) {
		return nil, errors.New("invalid JSON request")
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	err := decoder.Decode(&payload)
	return payload, err
}

// normalizeGeminiToolPayloads repairs alternate encodings without changing
// tool values, inventing names, or modifying already valid signed history.
func normalizeGeminiToolPayloads(body []byte) []byte {
	if !bytes.Contains(body, []byte(`"functionCall"`)) && !bytes.Contains(body, []byte(`"functionResponse"`)) {
		return body
	}
	payload, err := decodeGeminiRequestObject(body)
	if err != nil {
		return body
	}
	contents, _ := payload["contents"].([]any)
	var entries []map[string]any
	namesByID := make(map[string]map[string]bool)
	modified := false
	for _, rawContent := range contents {
		content, _ := rawContent.(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, rawPart := range parts {
			part, _ := rawPart.(map[string]any)
			for _, key := range []string{"functionCall", "functionResponse"} {
				entry, ok := part[key].(map[string]any)
				if !ok {
					continue
				}
				entries = append(entries, entry)
				id, _ := entry["id"].(string)
				name, _ := entry["name"].(string)
				if id != "" && strings.TrimSpace(name) != "" {
					if namesByID[id] == nil {
						namesByID[id] = make(map[string]bool)
					}
					namesByID[id][name] = true
				}
				if key == "functionCall" {
					switch args := entry["args"].(type) {
					case nil:
						entry["args"] = map[string]any{}
						modified = true
					case string:
						if json.Valid([]byte(args)) {
							if object, err := decodeGeminiRequestObject([]byte(args)); err == nil && object != nil {
								entry["args"] = object
								modified = true
							}
						}
					}
				} else if _, ok := entry["response"].(map[string]any); !ok {
					value, exists := entry["response"]
					if !exists {
						continue
					}
					if text, ok := value.(string); ok && json.Valid([]byte(text)) {
						if object, err := decodeGeminiRequestObject([]byte(text)); err == nil && object != nil {
							entry["response"] = object
							modified = true
							continue
						}
					}
					// Gemini requires a JSON object; retain scalar/array results as
					// the output value rather than dropping or stringifying them.
					entry["response"] = map[string]any{"output": value}
					modified = true
				}
			}
		}
	}
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		id, _ := entry["id"].(string)
		if strings.TrimSpace(name) == "" && id != "" && len(namesByID[id]) == 1 {
			for name := range namesByID[id] {
				entry["name"] = name
				modified = true
			}
		}
	}
	if !modified {
		return body
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return out
}

const geminiAttemptRequestKey = "gemini_attempt_request_shape_source"

type geminiAttemptRequest struct {
	Body  []byte
	Model string
}

func setGeminiAttemptRequest(c *gin.Context, body []byte, model string) {
	if c != nil {
		SetOpsUpstreamModel(c, model)
		c.Set(geminiAttemptRequestKey, geminiAttemptRequest{Body: body, Model: model})
	}
}

// Only fixed labels, counts, and numeric generation limits are persisted. No
// prompts, names, tool arguments/results, media bytes, URLs, or credentials.
type GeminiRequestShape struct {
	Model                string         `json:"model"`
	Contents             int            `json:"contents"`
	Tools                int            `json:"tools"`
	FunctionCalls        int            `json:"function_calls"`
	MissingCallNames     int            `json:"missing_call_names"`
	CallArgumentTypes    map[string]int `json:"call_argument_types,omitempty"`
	FunctionResponses    int            `json:"function_responses"`
	MissingResponseNames int            `json:"missing_response_names"`
	ResponseTypes        map[string]int `json:"response_types,omitempty"`
	GenerationFields     []string       `json:"generation_fields,omitempty"`
	MaxOutputTokens      *int64         `json:"max_output_tokens,omitempty"`
}

func geminiRequestShapeFromContext(c *gin.Context) *GeminiRequestShape {
	value, ok := c.Get(geminiAttemptRequestKey)
	if !ok {
		return nil
	}
	attempt, ok := value.(geminiAttemptRequest)
	if !ok {
		return nil
	}
	root := gjson.ParseBytes(attempt.Body)
	shape := &GeminiRequestShape{Model: attempt.Model, CallArgumentTypes: map[string]int{}, ResponseTypes: map[string]int{}}
	shape.Contents = len(root.Get("contents").Array())
	shape.Tools = len(root.Get("tools").Array())
	for _, content := range root.Get("contents").Array() {
		for _, part := range content.Get("parts").Array() {
			if call := part.Get("functionCall"); call.Exists() {
				shape.FunctionCalls++
				if strings.TrimSpace(call.Get("name").String()) == "" {
					shape.MissingCallNames++
				}
				shape.CallArgumentTypes[geminiJSONValueType(call.Get("args"))]++
			}
			if result := part.Get("functionResponse"); result.Exists() {
				shape.FunctionResponses++
				if strings.TrimSpace(result.Get("name").String()) == "" {
					shape.MissingResponseNames++
				}
				shape.ResponseTypes[geminiJSONValueType(result.Get("response"))]++
			}
		}
	}
	for _, key := range []string{"maxOutputTokens", "temperature", "topP", "topK", "thinkingConfig", "responseMimeType", "responseSchema", "responseJsonSchema", "responseModalities", "stopSequences", "candidateCount", "mediaResolution"} {
		if root.Get("generationConfig." + key).Exists() {
			shape.GenerationFields = append(shape.GenerationFields, key)
		}
	}
	if limit := root.Get("generationConfig.maxOutputTokens"); limit.Type == gjson.Number {
		n := limit.Int()
		shape.MaxOutputTokens = &n
	}
	return shape
}

func geminiJSONValueType(value gjson.Result) string {
	switch {
	case !value.Exists():
		return "missing"
	case value.IsObject():
		return "object"
	case value.IsArray():
		return "array"
	case value.Type == gjson.String:
		return "string"
	case value.Type == gjson.Number:
		return "number"
	case value.Type == gjson.True || value.Type == gjson.False:
		return "boolean"
	default:
		return "null"
	}
}

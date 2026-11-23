package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeGeminiToolPayloads(t *testing.T) {
	tests := []struct{ name, input, path, want string }{
		{"JSON argument object", `{"contents":[{"parts":[{"functionCall":{"name":"echo","args":"{\"id\":9007199254740993}"}}]}]}`, "contents.0.parts.0.functionCall.args.id", "9007199254740993"},
		{"null arguments", `{"contents":[{"parts":[{"functionCall":{"name":"echo","args":null}}]}]}`, "contents.0.parts.0.functionCall.args", "{}"},
		{"missing arguments", `{"contents":[{"parts":[{"functionCall":{"name":"echo"}}]}]}`, "contents.0.parts.0.functionCall.args", "{}"},
		{"string result", `{"contents":[{"parts":[{"functionResponse":{"name":"echo","response":"test"}}]}]}`, "contents.0.parts.0.functionResponse.response.output", "test"},
		{"JSON result object", `{"contents":[{"parts":[{"functionResponse":{"name":"echo","response":"{\"id\":9007199254740993}"}}]}]}`, "contents.0.parts.0.functionResponse.response.id", "9007199254740993"},
		{"array result", `{"contents":[{"parts":[{"functionResponse":{"name":"echo","response":[1,9007199254740993]}}]}]}`, "contents.0.parts.0.functionResponse.response.output.1", "9007199254740993"},
		{"null result", `{"contents":[{"parts":[{"functionResponse":{"name":"echo","response":null}}]}]}`, "contents.0.parts.0.functionResponse.response.output", ""},
		{"name from result ID", `{"contents":[{"parts":[{"functionCall":{"id":"call1","args":{}}}]},{"parts":[{"functionResponse":{"id":"call1","name":"echo","response":{}}}]}]}`, "contents.0.parts.0.functionCall.name", "echo"},
		{"name from call ID", `{"contents":[{"parts":[{"functionCall":{"id":"call1","name":"echo","args":{}}}]},{"parts":[{"functionResponse":{"id":"call1","response":{}}}]}]}`, "contents.1.parts.0.functionResponse.name", "echo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := normalizeGeminiToolPayloads([]byte(tt.input))
			require.True(t, json.Valid(out))
			require.Equal(t, tt.want, gjson.GetBytes(out, tt.path).String())
			require.Equal(t, string(out), string(normalizeGeminiToolPayloads(out)), "must be idempotent")
		})
	}
}

func TestNormalizeGeminiToolPayloadsPreservesValidAndAmbiguousHistory(t *testing.T) {
	for _, body := range []string{
		`{"contents":[{"parts":[{"text":"keep"},{"thoughtSignature":"signed","functionCall":{"id":"1","name":"echo","args":{"id":9007199254740993}}}]},{"parts":[{"functionResponse":{"id":"1","name":"echo","response":{"ok":true}}},{"inlineData":{"mimeType":"audio/wav","data":"keep"}}]}],"generationConfig":{"maxOutputTokens":123},"unknown":{"number":9007199254740993}}`,
		`{"contents":[{"parts":[{"functionCall":{"id":"1","name":"first","args":{}}},{"functionCall":{"id":"1","name":"second","args":{}}},{"functionResponse":{"id":"1","response":{}}}]}]}`,
		`{"contents":[{"parts":[{"functionCall":{"args":[1,2]}}]}]}`,
		`{"contents":[{"parts":[{"functionCall":{"name":"echo","args":"not JSON"}}]}]}`,
		`{"contents":[{"parts":[{"text":"functionCall"}]}]}`,
		`{"contents":[]} trailing`,
	} {
		require.Equal(t, body, string(normalizeGeminiToolPayloads([]byte(body))))
	}
}

func TestGeminiToolRewritesPreserveLargeNumbersAndMedia(t *testing.T) {
	body := []byte(`{"contents":[{"parts":[]},{"parts":[{"functionCall":{"id":"1","name":"echo","args":"{\"id\":9007199254740993}"}},{"inlineData":{"mimeType":"audio/wav","data":"exact_bytes"}}]},{"parts":[{"functionResponse":{"id":"1","response":"test"}}]}],"generationConfig":{"seed":9007199254740993}}`)
	out, err := filterEmptyPartsFromGeminiRequest(body)
	require.NoError(t, err)
	out = normalizeGeminiToolPayloads(out)
	out = ensureGeminiFunctionCallThoughtSignatures(out)
	out = CleanGeminiNativeThoughtSignatures(out)
	out = normalizeGeminiRequestForAIStudio(out)
	require.Equal(t, "9007199254740993", gjson.GetBytes(out, "contents.0.parts.0.functionCall.args.id").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(out, "generationConfig.seed").String())
	require.Equal(t, "exact_bytes", gjson.GetBytes(out, "contents.0.parts.1.inlineData.data").String())
	require.Equal(t, "echo", gjson.GetBytes(out, "contents.1.parts.0.functionResponse.name").String())
	require.Equal(t, "test", gjson.GetBytes(out, "contents.1.parts.0.functionResponse.response.output").String())
}

func TestGeminiNativeForwardRepairsToolPayloadBeforeDispatch(t *testing.T) {
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"OK"}]},"finishReason":"STOP"}]}`))}}}
	svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
	account := geminiCompatThinkingTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"gemini-3.8-flash": "gemini-3.1-flash-lite"}
	body := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"1","args":"{\"id\":9007199254740993}"}}]},{"role":"user","parts":[{"functionResponse":{"id":"1","name":"echo","response":"test"}}]}]}`)
	c, recorder := newAntigravityCompatContext(http.MethodPost, "/v1beta/models/gemini-3.8-flash:generateContent", body)
	result, err := svc.ForwardNative(context.Background(), c, account, "gemini-3.8-flash", "generateContent", false, body)
	require.NoError(t, err)
	require.Equal(t, 200, recorder.Code)
	require.Equal(t, "gemini-3.8-flash", result.Model)
	require.Equal(t, "gemini-3.1-flash-lite", c.GetString(OpsUpstreamModelKey))
	require.Len(t, upstream.requestBodies, 1)
	sent := upstream.requestBodies[0]
	require.Equal(t, "echo", gjson.GetBytes(sent, "contents.0.parts.0.functionCall.name").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(sent, "contents.0.parts.0.functionCall.args.id").String())
	require.Equal(t, "test", gjson.GetBytes(sent, "contents.1.parts.0.functionResponse.response.output").String())
}

func TestGeminiCompatConvertedToolPayloads(t *testing.T) {
	body := []byte(`{"max_tokens":256,"temperature":0.25,"top_p":0.9,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"1","name":"echo","input":"{\"id\":9007199254740993}"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"1","content":"test"}]}]}`)
	out, err := convertClaudeMessagesToGeminiGenerateContent(body)
	require.NoError(t, err)
	require.Equal(t, "9007199254740993", gjson.GetBytes(out, "contents.0.parts.0.functionCall.args.id").String())
	require.Equal(t, "echo", gjson.GetBytes(out, "contents.1.parts.0.functionResponse.name").String())
	require.Equal(t, int64(256), gjson.GetBytes(out, "generationConfig.maxOutputTokens").Int())
	require.Equal(t, 0.25, gjson.GetBytes(out, "generationConfig.temperature").Float())
	require.Equal(t, 0.9, gjson.GetBytes(out, "generationConfig.topP").Float())
}

func TestGeminiRequestShapeDoesNotPersistCustomerValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := []byte(`{"contents":[{"parts":[{"text":"PRIVATE_PROMPT"},{"functionCall":{"id":"PRIVATE_ID","name":"PRIVATE_NAME","args":{"value":"PRIVATE_ARGUMENT"}}},{"functionResponse":{"name":"PRIVATE_NAME","response":"PRIVATE_RESULT"}},{"inlineData":{"mimeType":"audio/wav","data":"PRIVATE_MEDIA"}}]}],"generationConfig":{"maxOutputTokens":100,"responseSchema":{"description":"PRIVATE_SCHEMA"},"PRIVATE_FIELD":"PRIVATE_VALUE"}}`)
	setGeminiAttemptRequest(c, body, "gemini-3.1-flash-lite")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: PlatformGemini, UpstreamStatusCode: 400})
	events := c.MustGet(OpsUpstreamErrorsKey).([]*OpsUpstreamErrorEvent)
	require.Len(t, events, 1)
	encoded, err := json.Marshal(events[0])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "PRIVATE_")
	require.Contains(t, string(encoded), `"response_types":{"string":1}`)
	require.Equal(t, "gemini-3.1-flash-lite", events[0].RequestShape.Model)
	require.Equal(t, int64(100), *events[0].RequestShape.MaxOutputTokens)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: PlatformOpenAI, UpstreamStatusCode: 400})
	events = c.MustGet(OpsUpstreamErrorsKey).([]*OpsUpstreamErrorEvent)
	require.Nil(t, events[1].RequestShape, "another platform must not reuse Gemini context")
}

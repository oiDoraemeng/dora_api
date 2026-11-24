//go:build unit

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

func TestAnthropicAPIKeyClaudeCodeCompatibilityPreservesRequestAndIsolatesSessions(t *testing.T) {
	account := newAnthropicAPIKeyAccountForTest()
	body := []byte(`{"model":"claude-opus-5-5","system":[{"type":"text","text":"Keep my instructions","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"plan","signature":"signed-history"},{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"ok"}]}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}],"metadata":{"tag":"preserve"}}`)
	original := string(body)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set("x-api-key", "client-one")
	require.Equal(t, body, prepareClaudeCodeAPIKeyCompatibilityBody(c, account, body), "disabled by default")
	account.Credentials["claude_code_compatibility"] = true
	out := prepareClaudeCodeAPIKeyCompatibilityBody(c, account, body)
	require.Equal(t, original, string(body), "do not mutate request shared with other account attempts")
	require.Equal(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(out, "messages").Raw)
	require.Equal(t, gjson.GetBytes(body, "tools").Raw, gjson.GetBytes(out, "tools").Raw)
	require.Equal(t, gjson.GetBytes(body, "system.0").Raw, gjson.GetBytes(out, "system.1").Raw)
	require.Equal(t, "preserve", gjson.GetBytes(out, "metadata.tag").String())
	uid := gjson.GetBytes(out, "metadata.user_id").String()
	require.NotNil(t, ParseMetadataUserID(uid))
	require.NotContains(t, uid, "client-one")
	require.Equal(t, out, prepareClaudeCodeAPIKeyCompatibilityBody(c, account, out), "must be idempotent")
	first := prepareClaudeCodeAPIKeyCompatibilityBody(c, account, []byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	later := prepareClaudeCodeAPIKeyCompatibilityBody(c, account, []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"OK"},{"role":"user","content":"next turn"}]}`))
	require.Equal(t, gjson.GetBytes(first, "metadata.user_id").String(), gjson.GetBytes(later, "metadata.user_id").String())
	c.Request.Header.Set("x-api-key", "client-two")
	other := prepareClaudeCodeAPIKeyCompatibilityBody(c, account, body)
	require.NotEqual(t, uid, gjson.GetBytes(other, "metadata.user_id").String())
	account.Platform = PlatformOpenAI
	require.Equal(t, body, prepareClaudeCodeAPIKeyCompatibilityBody(c, account, body))
	account.Platform, account.Type = PlatformAnthropic, AccountTypeOAuth
	require.False(t, account.IsClaudeCodeCompatibilityEnabled())
}

func TestAnthropicAPIKeyClaudeCodeCompatibilityForEveryIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"messages", "messages/count_tokens", "chat/completions", "responses"} {
		for _, stream := range []bool{false, true} {
			t.Run(endpoint+map[bool]string{false: "/buffered", true: "/streaming"}[stream], func(t *testing.T) {
				account := newAnthropicAPIKeyAccountForTest()
				account.Credentials["claude_code_compatibility"] = true
				payload := namespaceToolAnthropicStream()
				if endpoint == "messages/count_tokens" {
					payload = `{"input_tokens":5}`
				} else if endpoint == "messages" && !stream {
					payload = `{"type":"message","role":"assistant","id":"msg1","model":"claude-opus-5-5","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1}}`
				}
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}}}
				upstream.onCall = func(req *http.Request, stub *queuedHTTPUpstreamStub) {
					var sent map[string]any
					require.NoError(t, json.Unmarshal(stub.requestBodies[len(stub.requestBodies)-1], &sent))
					// Receiving HTTP servers canonicalize raw wire header casing.
					received := req.Clone(req.Context())
					received.Header = http.Header{}
					for name, values := range req.Header {
						for _, value := range values {
							received.Header.Add(name, value)
						}
					}
					require.True(t, NewClaudeCodeValidator().Validate(received, sent), "upstream must recognize the complete Claude Code envelope")
					require.Equal(t, "upstream-anthropic-key", getHeaderRaw(req.Header, "x-api-key"))
					require.Equal(t, "Keep my instructions", gjson.GetBytes(stub.requestBodies[0], "system.1.text").String())
				}
				svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				request := map[string]any{"model": "claude-opus-5-5", "max_tokens": 16, "stream": stream,
					"messages": []map[string]string{{"role": "system", "content": "Keep my instructions"}, {"role": "user", "content": "hello"}},
					"input":    "hello", "reasoning_effort": "min", "reasoning": map[string]string{"effort": "min"}}
				if endpoint == "responses" {
					request["instructions"] = "Keep my instructions"
				}
				if endpoint == "messages" || endpoint == "messages/count_tokens" {
					request["system"] = "Keep my instructions"
					request["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
				}
				body, err := json.Marshal(request)
				require.NoError(t, err)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(string(body)))
				c.Request.Header.Set("User-Agent", "ordinary-openai-client/1.0")
				c.Request.Header.Set("x-api-key", "downstream-secret")
				switch endpoint {
				case "messages", "messages/count_tokens":
					parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, parseErr)
					if endpoint == "messages" {
						_, err = svc.Forward(context.Background(), c, account, parsed)
					} else {
						err = svc.ForwardCountTokens(context.Background(), c, account, parsed)
					}
				case "chat/completions":
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
				case "responses":
					_, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
				}
				require.NoError(t, err)
				require.Equal(t, 1, upstream.callCount)
			})
		}
	}
}

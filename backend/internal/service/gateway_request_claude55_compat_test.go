//go:build unit

package service

import (
	"context"
	"fmt"
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

func TestClaude55AdaptationPreservesHistoryAndEffort(t *testing.T) {
	for _, target := range []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"} {
		for _, mode := range []string{"enabled", "disabled", "between_tools", "adaptive"} {
			for _, effort := range []string{"high", "max"} {
				t.Run(target+"/"+mode+"/"+effort, func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{"model":"public-model","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"history","signature":"signed"},{"type":"tool_use","id":"t1","name":"lookup","input":{}}]}],"thinking":{"type":%q,"budget_tokens":8000},"output_config":{"effort":%q},"temperature":0.4,"top_p":0.5,"top_k":20}`, mode, effort))
					original := string(body)
					out, _ := adaptClaude55Request(body, target)
					require.NoError(t, validateClaude55Request(out, target))
					require.Equal(t, original, string(body))
					require.Equal(t, "public-model", gjson.GetBytes(out, "model").String())
					require.Equal(t, effort, gjson.GetBytes(out, "output_config.effort").String())
					require.JSONEq(t, gjson.GetBytes(body, "messages").Raw, gjson.GetBytes(out, "messages").Raw)
					require.False(t, gjson.GetBytes(out, "thinking.budget_tokens").Exists())
					second, changed := adaptClaude55Request(out, target)
					require.False(t, changed, "adaptation must be idempotent")
					require.Equal(t, out, second)
				})
			}
		}
	}
}

func TestClaude55AdaptationIsolatesAccountAttempts(t *testing.T) {
	body := []byte(`{"model":"public-model","max_tokens":16000,"temperature":0.5,"thinking":{"type":"enabled","budget_tokens":8000},"messages":[{"role":"user","content":"hi"}]}`)
	original := string(body)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	accepted := false
	parsed.OnUpstreamAccepted = func() { accepted = true }
	first, err := adaptClaude55ParsedRequest(parsed, "claude-haiku-5-5")
	require.NoError(t, err)
	require.NotSame(t, parsed, first)
	require.True(t, first.ThinkingEnabled)
	first.OnUpstreamAccepted()
	require.True(t, accepted)
	require.Equal(t, original, string(parsed.Body.Bytes()))
	second, err := adaptClaude55ParsedRequest(parsed, "claude-sonnet-5")
	require.NoError(t, err)
	require.Equal(t, 0.5, gjson.GetBytes(second.Body.Bytes(), "temperature").Float())
	require.Equal(t, "enabled", gjson.GetBytes(second.Body.Bytes(), "thinking.type").String())
	require.Equal(t, "public-model", first.Model)
}

func TestClaude55DirectAndMappedForwardCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, target := range []string{"claude-sonnet-5-5", "claude-haiku-5-5", "claude-opus-5-5"} {
		for _, requested := range []string{target, "public-model"} {
			for _, endpoint := range []string{"messages", "messages/count_tokens", "chat/completions", "responses"} {
				for _, stream := range []bool{false, true} {
					if endpoint == "messages/count_tokens" && stream {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/%s/stream=%t", target, requested, endpoint, stream), func(t *testing.T) {
						body := []byte(fmt.Sprintf(`{"model":%q,"max_tokens":16000,"max_output_tokens":16000,"stream":%t,"temperature":0.5,"top_p":0.8,"thinking":{"type":"enabled","budget_tokens":8000},"input":"hi","messages":[{"role":"user","content":"hi"}]}`, requested, stream))
						original := string(body)
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(original))
						payload := namespaceToolAnthropicStream()
						contentType := "text/event-stream"
						if endpoint == "messages" && !stream {
							payload = fmt.Sprintf(`{"type":"message","role":"assistant","id":"msg1","model":%q,"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`, target)
							contentType = "application/json"
						} else if endpoint == "messages/count_tokens" {
							payload, contentType = `{"input_tokens":5}`, "application/json"
						}
						upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}}
						svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
						account := newAnthropicAPIKeyAccountForTest()
						account.Credentials["model_mapping"] = map[string]any{requested: target}
						parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
						require.NoError(t, err)
						switch endpoint {
						case "messages":
							_, err = svc.Forward(context.Background(), c, account, parsed)
						case "messages/count_tokens":
							err = svc.ForwardCountTokens(context.Background(), c, account, parsed)
						case "chat/completions":
							_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
						case "responses":
							_, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
						}
						require.NoError(t, err)
						require.NotNil(t, upstream.lastReq)
						require.Equal(t, target, gjson.GetBytes(upstream.lastBody, "model").String())
						require.Equal(t, original, string(parsed.Body.Bytes()))
						if target != "claude-opus-5-5" {
							require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
							require.False(t, gjson.GetBytes(upstream.lastBody, "top_p").Exists())
						}
						// Native Messages is rewritten at the shared handler writer;
						// the two protocol bridges also preserve the name here.
						if endpoint == "chat/completions" || endpoint == "responses" {
							require.Contains(t, rec.Body.String(), `"model":"`+requested+`"`)
						}
					})
				}
			}
		}
	}
}

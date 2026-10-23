package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type requestedModelTestUpstream struct {
	service.HTTPUpstream
	t             *testing.T
	stream        bool
	upstreamModel string
}

func (u *requestedModelTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	require.NoError(u.t, err)
	require.Equal(u.t, "/v1/chat/completions", req.URL.Path)
	require.Equal(u.t, u.upstreamModel, gjson.GetBytes(body, "model").String())
	// The provider can map internally even when this gateway has no mapping.
	payload := `{"id":"chatcmpl_test","object":"chat.completion","model":"grok-4.6","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":2,"total_tokens":11}}`
	contentType := "application/json"
	if u.stream {
		payload = "data: " + payload + "\n\ndata: [DONE]\n\n"
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
}

func TestRequestedModelResponseGrokRawForwarding(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, mapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/gateway_mapping=%v", stream, mapped), func(t *testing.T) {
				cfg := &config.Config{}
				bodyModel, upstreamModel := "grok-4.7", "grok-4.7"
				credentials := map[string]any{"api_key": "test-only", "base_url": "https://api.x.ai"}
				if mapped {
					bodyModel, upstreamModel = "channel-grok", "grok-4.6"
					credentials["model_mapping"] = map[string]any{"channel-grok": "grok-4.6"}
				}
				upstream := &requestedModelTestUpstream{t: t, stream: stream, upstreamModel: upstreamModel}
				svc := service.NewOpenAIGatewayService(
					nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
					upstream, nil, nil, nil, nil, nil, nil, nil, nil, nil,
				)
				account := &service.Account{ID: 1, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey, Credentials: credentials}
				router := gin.New()
				router.Use(InboundEndpointMiddleware())
				router.POST("/v1/chat/completions", func(c *gin.Context) {
					setOpsRequestContext(c, "grok-4.7", stream)
					// Exercise both a channel+account mapping chain and no local
					// mapping at all, with the provider returning grok-4.6 in both.
					body := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":%v}`, bodyModel, stream))
					result, err := svc.ForwardAsChatCompletions(c.Request.Context(), c, account, body, "", "")
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, upstreamModel, result.UpstreamModel)
					require.Equal(t, 9, result.Usage.InputTokens)
					require.Equal(t, 2, result.Usage.OutputTokens)
				})
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
				payload := strings.Split(strings.TrimPrefix(rec.Body.String(), "data: "), "\n")[0]
				require.Equal(t, "grok-4.7", gjson.Get(payload, "model").String())
			})
		}
	}
}

func TestRequestedModelResponseAllGatewayRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{
		"/v1/chat/completions", "/chat/completions", "/v1/messages", "/antigravity/v1/messages",
		"/v1/responses", "/v1/responses/compact", "/responses", "/responses/compact", "/backend-api/codex/responses",
		"/v1/embeddings", "/embeddings", "/v1/alpha/search", "/alpha/search",
		"/v1beta/models/gemini-alias:generateContent", "/antigravity/v1beta/models/gemini-alias:streamGenerateContent",
		"/v1/images/generations", "/v1/images/edits", "/v1/videos/generations", "/v1/videos/edits", "/v1/videos/extensions",
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", path, stream), func(t *testing.T) {
				router := gin.New()
				router.Use(InboundEndpointMiddleware())
				router.POST(path, func(c *gin.Context) {
					setOpsRequestContext(c, "", stream)
					setOpsRequestContext(c, "grok-4.7", stream)
					// Simulate channel/account mapping and retries after capture.
					setOpsRequestContext(c, "grok-4.6", stream)
					body := `{"model":"provider/canonical","usage":{"total_tokens":7},"choices":[{"delta":{"content":"grok-4.6"}}]}`
					if stream {
						c.Header("Content-Type", "text/event-stream")
						body = "data: " + body + "\n\ndata: [DONE]\n\n"
					} else {
						c.Header("Content-Type", "application/json")
					}
					c.Header("Content-Length", fmt.Sprint(len(body)))
					c.Header("ETag", "upstream-etag")
					c.Writer.WriteHeaderNow()
					// Fragment all metadata, including the model name and SSE delimiters.
					for _, b := range []byte(body) {
						n, err := c.Writer.Write([]byte{b})
						require.NoError(t, err)
						require.Equal(t, 1, n)
					}
				})
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
				require.Equal(t, http.StatusOK, rec.Code)
				require.Empty(t, rec.Header().Get("Content-Length"))
				require.Empty(t, rec.Header().Get("ETag"))
				payload := rec.Body.String()
				if stream {
					require.True(t, strings.HasSuffix(payload, "\n\ndata: [DONE]\n\n"))
					payload = strings.Split(strings.TrimPrefix(payload, "data: "), "\n")[0]
				}
				require.Equal(t, "grok-4.7", gjson.Get(payload, "model").String())
				require.Equal(t, int64(7), gjson.Get(payload, "usage.total_tokens").Int())
				require.Equal(t, "grok-4.6", gjson.Get(payload, "choices.0.delta.content").String())
			})
		}
	}
}

func TestRequestedModelResponseStreamingFlush(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(InboundEndpointMiddleware())
	rec := httptest.NewRecorder()
	router.POST("/v1/messages", func(c *gin.Context) {
		setOpsRequestContext(c, "client", true)
		c.Header("Content-Type", "text/event-stream")
		_, err := c.Writer.WriteString(": ping\r\n\r\n")
		require.NoError(t, err)
		c.Writer.Flush()
		require.True(t, rec.Flushed)
		require.Equal(t, ": ping\r\n\r\n", rec.Body.String())
		_, err = c.Writer.WriteString("event: message_start\r\ndata: {\r\ndata: \"message\":{\"model\":\"upstream\"}}\r\n\r\n")
		require.NoError(t, err)
		c.Writer.Flush()
		require.Contains(t, rec.Body.String(), `"model":"client"`)
		// Final event without a delimiter is still restored on handler return.
		_, err = c.Writer.WriteString("data: {\"model\":\"upstream\"}")
		require.NoError(t, err)
	})
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	require.True(t, strings.HasSuffix(rec.Body.String(), `data: {"model":"client"}`))
}

func TestRequestedModelResponsePreservesOtherResponses(t *testing.T) {
	for _, tc := range []struct {
		name, method, contentType, model, body string
		status                                 int
	}{
		{"model catalogue", http.MethodGet, "application/json", "client", `{"model":"upstream"}`, 200},
		{"error", http.MethodPost, "application/json", "client", `{"model":"upstream","error":{"message":"upstream unavailable"}}`, 429},
		{"missing model", http.MethodPost, "application/json", "", `{"model":"upstream"}`, 200},
		{"binary download", http.MethodPost, "application/octet-stream", "client", "binary\x00model", 200},
		{"whitespace keepalive", http.MethodPost, "application/json", "client", " \n{\"model\":\"upstream\"}", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(InboundEndpointMiddleware())
			router.Handle(tc.method, "/test", func(c *gin.Context) {
				setOpsRequestContext(c, tc.model, false)
				c.Data(tc.status, tc.contentType, []byte(tc.body))
			})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(tc.method, "/test", nil))
			require.Equal(t, tc.status, rec.Code)
			if tc.name == "whitespace keepalive" {
				require.Equal(t, "client", gjson.Get(rec.Body.String(), "model").String())
			} else {
				require.Equal(t, tc.body, rec.Body.String())
			}
		})
	}
}

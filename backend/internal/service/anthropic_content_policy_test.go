//go:build unit

package service

import (
	"context"
	"errors"
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

const anthropicContentAudit403 = `{"error":{"message":"内容审计命中风险规则，请调整输入后重试","type":"new_api_error"}}`

func TestAnthropicContentPolicyNeverPenalizesAccount(t *testing.T) {
	for _, mode := range []string{"default", "custom errors", "temp rule", "pool"} {
		t.Run(mode, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 43936, 1)
			h.account.Platform, h.account.Type = PlatformAnthropic, AccountTypeAPIKey
			h.account.Credentials = map[string]any{}
			switch mode {
			case "custom errors":
				h.account.Credentials["custom_error_codes_enabled"] = true
				h.account.Credentials["custom_error_codes"] = []any{float64(403)}
			case "pool":
				h.account.Credentials["pool_mode"] = true
				fallthrough
			case "temp rule":
				h.account.Credentials["temp_unschedulable_enabled"] = true
				h.account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
					"error_code": float64(403), "keywords": []any{"内容审计"}, "duration_minutes": float64(30),
				}}
			}
			for _, payload := range []string{anthropicContentAudit403, openAIContentPolicy403Body} {
				body := []byte(payload)
				require.Equal(t, ErrorPolicySkipped, h.svc.CheckErrorPolicy(context.Background(), h.account, 403, body))
				require.False(t, h.svc.HandleTempUnschedulable(context.Background(), h.account, 403, body))
				require.False(t, h.svc.HandleUpstreamError(context.Background(), h.account, 403, nil, body))
			}
			h.requireNoAccountPenalty(t)
		})
	}
}

func TestAnthropicAccountFailuresStillDisable(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"Insufficient account balance"}}`,
		`{"error":{"message":"insufficient user quota, remaining quota: -1"}}`,
		`{"error":{"code":"invalid_api_key","message":"内容审计命中风险规则，请调整输入后重试"}}`,
		`{"error":{"message":"Permission denied"},"request":{"message":"内容审计命中风险规则，请调整输入后重试"}}`,
	} {
		h := newOpenAI403TestHarness(t, 43936, 1)
		h.account.Platform = PlatformAnthropic
		require.False(t, isAccountContentPolicyRejection(h.account, 403, []byte(body)))
		require.True(t, h.svc.HandleUpstreamError(context.Background(), h.account, 403, nil, []byte(body)))
		require.Equal(t, 1, h.repo.setErrorCalls)
	}
}

func TestAnthropicContentPolicyReturns403WithoutRetryOrFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"messages", "chat/completions", "responses"} {
		for _, customRetry := range []bool{false, true} {
			t.Run(endpoint+map[bool]string{false: "/default", true: "/custom-retry"}[customRetry], func(t *testing.T) {
				h := newOpenAI403TestHarness(t, 43936, 1)
				h.account = newAnthropicAPIKeyAccountForTest()
				if customRetry {
					h.account.Credentials["custom_error_codes_enabled"] = true
					h.account.Credentials["custom_error_codes"] = []any{float64(401)}
				}
				upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
					StatusCode: 403, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(anthropicContentAudit403)),
				}}}
				svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream, rateLimitService: h.svc}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				body := []byte(`{"model":"claude-opus-5-5","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"input":"hello"}`)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(string(body)))
				var err error
				switch endpoint {
				case "messages":
					parsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
					require.NoError(t, parseErr)
					_, err = svc.Forward(context.Background(), c, h.account, parsed)
				case "chat/completions":
					_, err = svc.ForwardAsChatCompletions(context.Background(), c, h.account, body, nil)
				case "responses":
					_, err = svc.ForwardAsResponses(context.Background(), c, h.account, body, nil)
				}
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
				require.Equal(t, 1, upstream.callCount)
				require.Equal(t, 403, recorder.Code)
				require.Equal(t, "content_policy_violation", gjson.GetBytes(recorder.Body.Bytes(), "error.code").String())
				require.True(t, IsResponseCommitted(c))
				h.requireNoAccountPenalty(t)
			})
		}
	}
}

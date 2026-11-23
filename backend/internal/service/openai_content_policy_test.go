//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAIContentPolicy403Body = `{"error":{"code":"content_policy_violation","message":"Request rejected by the content moderation policy. (request id: 202611231626525326590298268d9d6xcMicEnN)","param":"","type":"new_api_error"}}`

func TestIsOpenAIContentPolicyRejection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"production rejection", 403, openAIContentPolicy403Body, true},
		{"message only", 403, `{"error":{"message":"Request rejected by the content moderation policy."}}`, true},
		{"plain text", 403, "Request rejected by the content moderation policy.", true},
		{"filter code", 403, `{"error":{"code":"content_filter","message":"Blocked"}}`, true},
		{"nested response", 403, `{"response":{"error":{"code":"content_policy_violation"}}}`, true},
		{"type marker", 403, `{"error":{"type":"content_moderation_error"}}`, true},
		{"account policy", 403, `{"error":{"message":"Workspace is forbidden by policy"}}`, false},
		{"generic reject", 403, `{"error":{"message":"Request rejected"}}`, false},
		{"credentials rejected", 403, `{"error":{"code":"invalid_api_key","message":"Request rejected by the content moderation policy."}}`, false},
		{"workspace disabled", 403, `{"error":{"code":"workspace_disabled","message":"Request rejected by the content moderation policy."}}`, false},
		{"account disabled type", 403, `{"error":{"code":"content_filter","type":"account_disabled"}}`, false},
		{"workspace disabled type", 403, `{"error":{"code":"content_filter","type":"workspace_disabled"}}`, false},
		{"account disabled message", 403, `{"error":{"code":"content_filter","message":"Account has been disabled due to content policy violation"}}`, false},
		{"subscription", 403, `{"error":{"code":"subscription_required","message":"content policy violation"}}`, false},
		{"echoed request marker", 403, `{"error":{"message":"Permission denied"},"request":{"code":"content_policy_violation","message":"Request rejected by the content moderation policy."}}`, false},
		{"authentication status", 401, openAIContentPolicy403Body, false},
		{"quota status", 429, openAIContentPolicy403Body, false},
		{"server status", 503, openAIContentPolicy403Body, false},
		{"html forbidden", 403, openAI403HTMLBody, false},
		{"empty", 403, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isOpenAIContentPolicyRejection(tc.status, []byte(tc.body)))
		})
	}
}

func TestOpenAIContentPolicy403NeverPenalizesAccount(t *testing.T) {
	for _, mode := range []string{"default", "explicit temp rule", "custom error codes", "pool with temp rule", "missing counter cache"} {
		t.Run(mode, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 43795, 1, 2, 3, 4, 5)
			h.account.Type = AccountTypeAPIKey
			h.account.Credentials = map[string]any{}
			if mode == "missing counter cache" {
				h.svc.SetOpenAI403CounterCache(nil)
			}
			if mode == "explicit temp rule" || mode == "pool with temp rule" {
				h.account.Credentials["temp_unschedulable_enabled"] = true
				h.account.Credentials["temp_unschedulable_rules"] = []any{map[string]any{
					"error_code": float64(403), "keywords": []any{"rejected"}, "duration_minutes": float64(30),
				}}
			}
			if mode == "custom error codes" {
				h.account.Credentials["custom_error_codes_enabled"] = true
				h.account.Credentials["custom_error_codes"] = []any{float64(403)}
			}
			if mode == "pool with temp rule" {
				h.account.Credentials["pool_mode"] = true
			}
			body := []byte(openAIContentPolicy403Body)
			gateway := &OpenAIGatewayService{accountRepo: h.repo, rateLimitService: h.svc}
			for i := 0; i < 5; i++ {
				require.Equal(t, ErrorPolicySkipped, h.svc.CheckErrorPolicy(context.Background(), h.account, 403, body, "gpt-5.2"))
				require.False(t, h.svc.HandleTempUnschedulable(context.Background(), h.account, 403, body, "gpt-5.2"))
				require.False(t, h.handle(openAIContentPolicy403Body))
				require.False(t, gateway.handleOpenAIAccountUpstreamError(context.Background(), h.account, 403, nil, body, "gpt-5.2"))
			}
			h.requireNoAccountPenalty(t)
			require.False(t, gateway.isOpenAIAccountRuntimeBlocked(h.account))
		})
	}
}

func TestOpenAIContentPolicy403ReturnsRefusalWithoutFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, format := range []string{"responses", "chat/completions", "messages"} {
		t.Run(format, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 43793, 1)
			svc := &OpenAIGatewayService{accountRepo: h.repo, rateLimitService: h.svc}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+format, nil)
			resp := &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(openAIContentPolicy403Body)),
			}
			body := []byte(openAIContentPolicy403Body)
			require.False(t, svc.shouldFailoverOpenAIUpstreamResponse(h.account, 403, "", body))
			require.Nil(t, svc.failoverOpenAIUpstreamHTTPError(context.Background(), c, h.account, resp, body, "", "gpt-5.2"))
			var err error
			switch format {
			case "responses":
				_, err = svc.handleErrorResponse(context.Background(), resp, c, h.account, nil)
			case "chat/completions":
				_, err = svc.handleChatCompletionsErrorResponse(resp, c, h.account)
			case "messages":
				_, err = svc.handleAnthropicErrorResponse(resp, c, h.account)
			}
			require.Error(t, err)
			require.Equal(t, 403, recorder.Code)
			require.True(t, IsResponseCommitted(c))
			require.Contains(t, recorder.Body.String(), "Request rejected by the content moderation policy")
			if format == "responses" {
				require.JSONEq(t, openAIContentPolicy403Body, recorder.Body.String())
			} else {
				require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
			}
			h.requireNoAccountPenalty(t)
		})
	}
}

func TestOpenAIContentPolicy403DoesNotAdvanceReal403Threshold(t *testing.T) {
	h := newOpenAI403TestHarness(t, 43795, 1)
	for i := 0; i < 4; i++ {
		require.False(t, h.handle(openAIContentPolicy403Body))
	}
	h.requireNoAccountPenalty(t)
	require.True(t, h.handle(`{"error":{"message":"Workspace is forbidden by policy"}}`))
	require.Equal(t, 1, h.counter.increments)
	require.Equal(t, 1, h.repo.tempCalls)
	require.Zero(t, h.repo.setErrorCalls)
	require.Contains(t, h.repo.lastTempReason, "temporary cooldown (1/3)")
}

func TestOpenAIContentPolicy403PassthroughDoesNotRotatePool(t *testing.T) {
	h := newOpenAI403TestHarness(t, 43217, 1)
	h.account.Type = AccountTypeAPIKey
	h.account.Credentials = map[string]any{"pool_mode": true}
	svc := &OpenAIGatewayService{accountRepo: h.repo, rateLimitService: h.svc}
	body := []byte(openAIContentPolicy403Body)
	require.False(t, shouldFailoverOpenAIPassthroughResponse(h.account, 403, body))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	err := svc.handleErrorResponsePassthrough(context.Background(), &http.Response{
		StatusCode: 403, Header: http.Header{},
	}, c, h.account, []byte(`{"model":"gpt-5.2"}`), body)
	require.Error(t, err)
	require.Equal(t, 403, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Request rejected by the content moderation policy")
	h.requireNoAccountPenalty(t)
}

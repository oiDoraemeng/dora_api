//go:build unit

package service

import (
	"context"
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

// issue #7919：claude-haiku-5-5 请求校验（官方 migration guide 口径）。
func TestValidateClaude55Request_Haiku55(t *testing.T) {
	const model = "claude-haiku-5-5"
	const prefix = `{"model":"claude-haiku-5-5","max_tokens":16000,"messages":[{"role":"user","content":"hi"}]`
	cases := []struct {
		name string
		// extra 为追加进最小合法请求的 JSON 片段（含前导逗号）
		extra   string
		wantErr string // 为空表示期望通过
	}{
		{name: "adaptive thinking passes", extra: `,"thinking":{"type":"adaptive"},"output_config":{"effort":"medium"}`},
		{name: "omitted thinking passes", extra: ``},
		{name: "enabled thinking rejected", extra: `,"thinking":{"type":"enabled","budget_tokens":8000}`, wantErr: "requires adaptive thinking"},
		{name: "拒绝 between_tools 并返回 Haiku 错误说明", extra: `,"thinking":{"type":"between_tools"}`, wantErr: "claude-haiku-5-5 requires adaptive thinking; thinking.type=between_tools is unsupported"},
		{name: "disabled thinking allowed at medium effort", extra: `,"thinking":{"type":"disabled"},"output_config":{"effort":"medium"}`},
		{name: "disabled thinking allowed when effort omitted", extra: `,"thinking":{"type":"disabled"}`},
		{name: "disabled thinking rejected at xhigh", extra: `,"thinking":{"type":"disabled"},"output_config":{"effort":"xhigh"}`, wantErr: "supports only low, medium or high effort"},
		{name: "disabled thinking rejected at max", extra: `,"thinking":{"type":"disabled"},"output_config":{"effort":"max"}`, wantErr: "supports only low, medium or high effort"},
		{name: "temperature 1 passes", extra: `,"temperature":1`},
		{name: "non-default temperature rejected", extra: `,"temperature":0.5`, wantErr: "non-default temperature"},
		{name: "top_p 0.99 passes", extra: `,"top_p":0.99`},
		{name: "拒绝 top_p=0.995", extra: `,"top_p":0.995`, wantErr: "non-default top_p"},
		{name: "拒绝 top_p=1", extra: `,"top_p":1`, wantErr: "non-default top_p"},
		{name: "拒绝字符串 top_p", extra: `,"top_p":"0.99"`, wantErr: "non-default top_p"},
		{name: "top_p 0.5 rejected", extra: `,"top_p":0.5`, wantErr: "non-default top_p"},
		{name: "top_k rejected", extra: `,"top_k":5`, wantErr: "does not support top_k"},
		{name: "temperature and top_p together rejected", extra: `,"temperature":1,"top_p":0.99`, wantErr: "does not allow temperature and top_p together"},
		{name: "forced tool_choice any allowed", extra: `,"tool_choice":{"type":"any"}`},
		{name: "forced tool_choice required allowed", extra: `,"tool_choice":"required"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(prefix + tc.extra + "}")
			err := validateClaude55Request(body, model)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.True(t, strings.Contains(err.Error(), tc.wantErr),
				"error %q should contain %q", err.Error(), tc.wantErr)
		})
	}
}

func TestMappedHaiku55AdaptsLegacyMessagesParameters(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-5","thinking":{"type":"enabled","budget_tokens":8000},"output_config":{"effort":"high"},"temperature":0.5,"top_p":0.9,"top_k":20,"messages":[{"role":"user","content":"hi"}]}`)
	target := "claude-haiku-5-5"
	adapted, changed := adaptMappedHaiku55Request(body, "claude-sonnet-5", target)
	require.True(t, changed)
	require.NoError(t, validateClaude55Request(adapted, target))
	require.Equal(t, "adaptive", gjson.GetBytes(adapted, "thinking.type").String())
	require.Equal(t, "high", gjson.GetBytes(adapted, "output_config.effort").String())
	require.Equal(t, "hi", gjson.GetBytes(adapted, "messages.0.content").String())
	for _, field := range []string{"temperature", "top_p", "top_k", "thinking.budget_tokens"} {
		require.False(t, gjson.GetBytes(adapted, field).Exists(), field)
		require.True(t, gjson.GetBytes(body, field).Exists(), field)
	}
	for _, models := range [][2]string{{"claude-haiku-5.5", target}, {"claude-opus-4-6", "claude-sonnet-5-5"}} {
		unmodified, changed := adaptMappedHaiku55Request(body, models[0], models[1])
		require.False(t, changed)
		require.Equal(t, body, unmodified)
	}
}

func TestMappedHaiku55MessagesForwardAdaptsPayload(t *testing.T) {
	const requested, target = "claude-sonnet-5", "claude-haiku-5-5"
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-sonnet-5","max_tokens":16000,"thinking":{"type":"enabled","budget_tokens":8000},"temperature":0.5,"messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"type":"message","model":"claude-haiku-5-5","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":5,"output_tokens":2}}`)),
	}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := newAnthropicAPIKeyAccountForTest()
	account.Credentials["model_mapping"] = map[string]any{requested: target}
	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, target, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "adaptive", gjson.GetBytes(upstream.lastBody, "thinking.type").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "thinking.budget_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
}

func TestForwardMappedClaude55ValidationMarksClientErrorForRewrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"public-model","temperature":"0.5","messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	account := &Account{
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"public-model": "claude-haiku-5-5"},
		},
	}

	_, err = (&GatewayService{}).Forward(context.Background(), c, account, parsed)
	require.ErrorContains(t, err, "claude-haiku-5-5")
	require.True(t, ShouldRewriteClientError(c))
	require.Contains(t, recorder.Body.String(), "claude-haiku-5-5")
}

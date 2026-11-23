//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const optionalImageToolBody = `{"model":"gpt-5.4","input":"Reply hello","tools":[{"type":"image_generation"}],"tool_choice":{"type":"image_generation"},"stream":false}`

func TestResponsesImageToolRoutingPrefersCatalogThenFallsBack(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, scenario := range []string{"image available", "image disabled", "image cooldown", "explicit tool model unavailable", "no image model"} {
			t.Run(scenario+map[bool]string{false: "/legacy", true: "/advanced"}[advanced], func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				text := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 10, Priority: 0}
				image := text
				image.ID = 2
				image.Priority = 10
				image.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4", "picture-alias": "gpt-image-2"}}
				body := []byte(optionalImageToolBody)
				switch scenario {
				case "image disabled":
					image.Schedulable = false
				case "image cooldown":
					setAccountModelRateLimitSnapshot(&image, openAIImageGenerationRateLimitKey, time.Now().Add(time.Minute), "denied", time.Now())
				case "explicit tool model unavailable":
					body = []byte(strings.Replace(optionalImageToolBody, `"type":"image_generation"}`, `"type":"image_generation","model":"gpt-image-2.5-flare"}`, 1))
				case "no image model":
					image.Credentials = map[string]any{"model_mapping": map[string]any{"gpt-5.4": "gpt-5.4", "gpt-image-2": "gpt-5.4"}}
				}
				ctx, _, err := PrepareOpenAIResponsesImageToolRequest(context.Background(), &Group{AllowImageGeneration: true}, "gpt-5.4", body, false)
				require.NoError(t, err)
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = true
				svc := &OpenAIGatewayService{cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{text, image}}, cache: &schedulerTestGatewayCache{sessionBindings: map[string]int64{"text-sticky": 1}}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
				if advanced {
					svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
				}
				groupID := int64(4)
				selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "text-sticky", "gpt-5.4", nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, false)
				require.NoError(t, err)
				require.NotNil(t, selection)
				if selection.ReleaseFunc != nil {
					defer selection.ReleaseFunc()
				}
				wantImage := scenario == "image available"
				if wantImage {
					require.Equal(t, int64(2), selection.Account.ID)
				}
				if scenario == "image disabled" {
					require.Equal(t, int64(1), selection.Account.ID)
				}
				forwarded, stripped, err := applyResponsesImageToolAccountFallback(ctx, selection.Account, body)
				require.NoError(t, err)
				require.Equal(t, !wantImage, stripped)
				require.Equal(t, wantImage, gjson.GetBytes(forwarded, `tools.#(type=="image_generation")`).Exists())
			})
		}
	}
}

func TestResponsesImageToolFallbackPreservesClientToolsAndInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":"unchanged"},{"type":"additional_tools","tools":[{"type":"image_generation"},{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]}],"tools":[{"type":"image_generation"},{"type":"function","name":"shell"}],"tool_choice":{"type":"allowed_tools","tools":[{"type":"image_generation"},{"type":"function","name":"shell"}]},"metadata":{"image_generation":"keep"}}`)
	updated, changed, err := stripNativeResponsesImageTool(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "unchanged", gjson.GetBytes(updated, "input.0.content").String())
	require.Equal(t, "image_gen", gjson.GetBytes(updated, "input.1.tools.0.name").String())
	require.Equal(t, "shell", gjson.GetBytes(updated, "tools.0.name").String())
	require.Equal(t, "shell", gjson.GetBytes(updated, "tool_choice.tools.0.name").String())
	require.Equal(t, "keep", gjson.GetBytes(updated, "metadata.image_generation").String())
	_, changed, err = stripNativeResponsesImageTool(updated)
	require.NoError(t, err)
	require.False(t, changed)
}

func TestResponsesImageToolFallbackRespectsRequiredProtocol(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, requireResponses := range []bool{false, true} {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 10, Extra: map[string]any{"openai_responses_supported": false}}
			require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses))
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = true
			svc := &OpenAIGatewayService{cfg: cfg, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}, cache: &schedulerTestGatewayCache{}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
			if advanced {
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
			}
			ctx, _, err := PrepareOpenAIResponsesImageToolRequest(context.Background(), &Group{AllowImageGeneration: true}, "gpt-5.4", []byte(optionalImageToolBody), requireResponses)
			require.NoError(t, err)
			groupID := int64(4)
			selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, &groupID, "", "", "gpt-5.4", nil, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, false)
			if requireResponses {
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				continue
			}
			require.NoError(t, err)
			require.NotNil(t, selection)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		}
	}
}

func TestResponsesImageToolFallbackDisabledGroupAndDedicatedModel(t *testing.T) {
	ctx, updated, err := PrepareOpenAIResponsesImageToolRequest(context.Background(), &Group{}, "gpt-5.4", []byte(optionalImageToolBody), false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(updated, "tools").Exists())
	require.False(t, gjson.GetBytes(updated, "tool_choice").Exists())
	_, enabled := responsesImageToolRoutingFromContext(ctx)
	require.False(t, enabled)
	_, updated, err = PrepareOpenAIResponsesImageToolRequest(context.Background(), &Group{}, "gpt-image-2", []byte(optionalImageToolBody), false)
	require.NoError(t, err)
	require.JSONEq(t, optionalImageToolBody, string(updated))
}

func TestResponsesImageToolFallbackForwardedBeforePassthroughAndConversion(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "passthrough"}[passthrough], func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"resp_text","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`))}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.cfg.Gateway.CodexImageGenerationBridgeEnabled = true
			c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.98.0")
			account := newOpenAIImageGenerationControlTestAccount()
			account.Extra = map[string]any{"openai_passthrough": passthrough}
			ctx, body, err := PrepareOpenAIResponsesImageToolRequest(context.Background(), &Group{AllowImageGeneration: true}, "gpt-5.4", []byte(optionalImageToolBody), false)
			require.NoError(t, err)
			SetOpenAIImageIntentHint(c, true)
			_, err = svc.Forward(ctx, c, account, body)
			require.NoError(t, err)
			require.NotNil(t, upstream.lastReq)
			require.False(t, gjson.GetBytes(upstream.lastBody, `tools.#(type=="image_generation")`).Exists())
			require.False(t, gjson.GetBytes(upstream.lastBody, "tool_choice").Exists())
		})
	}
}

func TestResponsesImageToolPassthroughSanitizesEveryTurn(t *testing.T) {
	for _, imageAvailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "text account", true: "image account"}[imageAvailable], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := newStagedPassthroughConn()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 10
			account := passthroughLifecycleAccount()
			if imageAvailable {
				account.Credentials["model_mapping"] = map[string]any{"gpt-5.4": "gpt-5.4", "gpt-image-2": "gpt-image-2"}
			}
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, newPassthroughLifecycleService(cfg, upstream), account, func(c *gin.Context) *OpenAIWSIngressHooks {
				c.Set("api_key", &APIKey{Group: &Group{AllowImageGeneration: true}})
				return nil
			})
			defer server.Close()
			payload := strings.Replace(optionalImageToolBody, `{"model"`, `{"type":"response.create","model"`, 1)
			client := dialPassthroughLifecycleClientWithPayload(t, server, payload)
			defer func() { _ = client.CloseNow() }()
			for turn := 0; turn < 2; turn++ {
				if turn > 0 {
					writeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
					err := client.Write(writeCtx, coderws.MessageText, []byte(payload))
					stop()
					require.NoError(t, err)
				}
				forwarded := requirePassthroughUpstreamWrite(t, upstream, 3*time.Second)
				require.Equal(t, imageAvailable, gjson.GetBytes(forwarded, `tools.#(type=="image_generation")`).Exists())
				require.Equal(t, imageAvailable, gjson.GetBytes(forwarded, "tool_choice").Exists())
				upstream.Send(`{"type":"response.completed","response":{"id":"resp_image_tool_test","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}}`)
				_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
			}
			_ = client.Close(coderws.StatusNormalClosure, "done")
			select {
			case <-serverErr:
			case <-time.After(3 * time.Second):
				t.Fatal("image tool passthrough session did not exit")
			}
		})
	}
}

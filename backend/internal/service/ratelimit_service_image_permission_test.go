//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const imagePermissionDeniedBody = `{"error":{"type":"permission_error","message":"Image generation is not enabled for this group"}}`

func TestImagePermissionDeniedNeverPenalizesWholeAccount(t *testing.T) {
	h := newOpenAI403TestHarness(t, 701, 1, 2, 3, 4)
	h.account.Type = AccountTypeAPIKey
	repo := &modelNotFoundAccountRepoStub{}
	h.svc.accountRepo = repo
	for range 4 {
		require.False(t, h.handle(imagePermissionDeniedBody))
	}
	h.requireNoAccountPenalty(t)
	require.Len(t, repo.modelRateLimitCalls, 4)
	for _, call := range repo.modelRateLimitCalls {
		require.Equal(t, openAIImageGenerationRateLimitKey, call.scope)
	}
	require.True(t, h.account.isModelRateLimitedWithContext(WithOpenAIImageGenerationIntent(context.Background()), "gpt-6-luna"))
	require.False(t, h.account.isModelRateLimitedWithContext(context.Background(), "gpt-6-luna"))
}

func TestImagePermissionDeniedGatewayDoesNotBlockTextScheduling(t *testing.T) {
	h := newOpenAI403TestHarness(t, 702, 1)
	repo := &modelNotFoundAccountRepoStub{}
	h.svc.accountRepo = repo
	svc := &OpenAIGatewayService{rateLimitService: h.svc}
	require.False(t, svc.handleOpenAIAccountUpstreamError(context.Background(), h.account, http.StatusForbidden, http.Header{}, []byte(imagePermissionDeniedBody), "gpt-6-luna"))
	h.requireNoAccountPenalty(t)
	require.Len(t, repo.modelRateLimitCalls, 1)
}

func TestImagePermissionDenialRequiresExactStructured403(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"real auth denial", `{"error":{"message":"account suspended"}}`, 403},
		{"quoted permission message", `{"error":{"message":"Account suspended: Image generation is not enabled for this group"}}`, 403},
		{"unstructured", `Image generation is not enabled for this group`, 403},
		{"different status", imagePermissionDeniedBody, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newOpenAI403TestHarness(t, 703, 1)
			repo := &modelNotFoundAccountRepoStub{}
			h.svc.accountRepo = repo
			require.False(t, h.svc.HandleOpenAIImagePermissionDenied(context.Background(), h.account, tc.status, []byte(tc.body)))
			require.Empty(t, repo.modelRateLimitCalls)
		})
	}
}

func TestCodexBridgeSkipsImagePermissionCooldown(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{featureKeyCodexImageGenerationBridge: true}}
	setAccountModelRateLimitSnapshot(account, openAIImageGenerationRateLimitKey, time.Now().Add(time.Minute), "permission denied", time.Now())
	svc := &OpenAIGatewayService{}
	require.False(t, svc.isCodexImageGenerationBridgeEnabled(context.Background(), account, nil))
}

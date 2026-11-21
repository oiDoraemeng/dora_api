//go:build unit

package handler

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type keywordSlotBindings struct {
	testutil.StubGatewayCache
	owner int64
	sets  int
}

func (s *keywordSlotBindings) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return s.owner, nil
}
func (s *keywordSlotBindings) SetSessionAccountID(_ context.Context, _ int64, _ string, owner int64, _ time.Duration) error {
	s.owner, s.sets = owner, s.sets+1
	return nil
}

func TestKeywordRoutingPreacquiredSlotBindsAndWaitUsesOriginalBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(&contentModerationHandlerSettingRepo{}, nil)
	_, err := settings.SetKeywordRoutingConfig(context.Background(), service.KeywordRoutingConfig{Keywords: []string{"hit"}})
	require.NoError(t, err)
	groupID := int64(24)
	on := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 2, GroupIDs: []int64{groupID}, Extra: map[string]any{service.KeywordRoutingEnabledExtraKey: true}}
	off := on
	off.ID, off.Extra = 2, nil
	bindings := &keywordSlotBindings{owner: off.ID}
	repo := openAIImagesFailoverAccountRepo{accounts: []service.Account{on, off}}
	gw := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, bindings, &config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, settings, nil)
	ctx := service.WithKeywordRoutingRequest(context.Background(), service.ContentModerationProtocolOpenAIResponses, []byte(`{"input":"hit"}`), &groupID)
	selection, _, err := gw.SelectAccountWithScheduler(ctx, &groupID, "", "session", "gpt-5.6-sol", nil, service.OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Equal(t, on.ID, selection.Account.ID)
	require.Zero(t, bindings.sets, "selection must defer rebinding until admission")

	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	h := &OpenAIGatewayHandler{gatewayService: gw}
	streamStarted := false
	release, result := h.acquireResponsesAccountSlot(c, &groupID, "session", selection, false, &streamStarted, zap.NewNop())
	require.Equal(t, openAISlotAcquireOK, result)
	require.NotNil(t, release)
	release()
	require.Equal(t, on.ID, bindings.owner)
	require.Equal(t, 1, bindings.sets, "keyword-only admission must replace the ordinary sticky owner")
	require.Zero(t, writer.Body.Len())

	state := NewFailoverState(2, false)
	state.SwitchCount = 1 // An upstream attempt has already consumed one switch.
	require.True(t, state.TryKeywordWaitFailover(ctx, &on))
	require.Equal(t, 2, state.SwitchCount)
	require.Contains(t, state.FailedAccountIDs, on.ID)
	require.False(t, state.TryKeywordWaitFailover(ctx, &on))
	require.False(t, NewFailoverState(2, false).TryKeywordWaitFailover(ctx, &off))
}

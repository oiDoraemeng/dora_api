//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestKeywordRoutingGatewayPools(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, platform := range []string{PlatformAnthropic, PlatformGemini, PlatformGrok, PlatformTypeSafe} {
			t.Run(fmt.Sprintf("%s/batch=%v", platform, batch), func(t *testing.T) {
				group := gatewayProfitTestGroup(900, platform)
				group.ProfitControlEnabled = false
				on := gatewayProfitTestAccount(1, platform, 1, group.ID)
				on.Extra = map[string]any{KeywordRoutingEnabledExtraKey: true}
				off := gatewayProfitTestAccount(2, platform, 1, group.ID)
				off.Priority = -10
				repo := &mockAccountRepoForPlatform{accounts: []Account{on, off}, accountsByID: map[int64]*Account{1: &on, 2: &off}}
				cfg := testConfig()
				cfg.Gateway.Scheduling.LoadBatchEnabled = batch
				stats := &keywordStatsStub{}
				settings := keywordSettings("画图")
				settings.keywordRoutingStats = stats
				svc := &GatewayService{accountRepo: repo, cache: &mockGatewayCacheForPlatform{}, cfg: cfg, settingService: settings, concurrencyService: NewConcurrencyService(stubConcurrencyCache{})}
				ctx := WithKeywordRoutingRequest(gatewayProfitTestContext(group), ContentModerationProtocolOpenAIResponses, []byte(`{"input":"画图"}`), &group.ID)
				selection, err := svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "", nil, "", 0)
				require.NoError(t, err)
				require.EqualValues(t, 1, selection.Account.ID)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				selection, err = svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "", map[int64]struct{}{1: {}}, "", 0)
				require.NoError(t, err)
				require.EqualValues(t, 2, selection.Account.ID)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				require.Equal(t, 1, stats.calls)
				ctx = WithKeywordRoutingRequest(gatewayProfitTestContext(group), ContentModerationProtocolOpenAIResponses, []byte(`{"input":"代码"}`), &group.ID)
				selection, err = svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "", nil, "", 0)
				require.NoError(t, err)
				require.EqualValues(t, 2, selection.Account.ID)
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				_, err = svc.SelectAccountWithLoadAwareness(ctx, &group.ID, "", "", map[int64]struct{}{2: {}}, "", 0)
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
			})
		}
	}
}
func TestKeywordRoutingOpenAISchedulersAndSticky(t *testing.T) {
	for _, advanced := range []string{"false", "true"} {
		t.Run(advanced, func(t *testing.T) {
			groupID := int64(901)
			on := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2, GroupIDs: []int64{groupID}, Extra: map[string]any{KeywordRoutingEnabledExtraKey: true}}
			off := on
			off.ID = 2
			off.Extra = nil
			off.Priority = -10
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session": 2}}
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{on, off}}, cache: cache, cfg: &config.Config{}, settingService: keywordSettings("画图"), rateLimitService: newOpenAIAdvancedSchedulerRateLimitService(advanced), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
			ctx := WithKeywordRoutingRequest(context.Background(), ContentModerationProtocolOpenAIResponses, []byte(`{"input":"画图"}`), &groupID)
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.EqualValues(t, 1, selection.Account.ID)
			require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ContextWithSelectionProfitGate(ctx, selection), &groupID, "session", selection.Account.ID))
			require.EqualValues(t, 1, cache.sessionBindings["openai:session"])
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			ctx = WithKeywordRoutingRequest(context.Background(), ContentModerationProtocolOpenAIResponses, []byte(`{"input":"代码"}`), &groupID)
			selection, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "session", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.EqualValues(t, 2, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			_, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "session", "gpt-5.1", map[int64]struct{}{2: {}}, OpenAIUpstreamTransportAny, false)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			// Non-migratable server-side response owners are the explicit exception.
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(context.Background(), groupID, "resp_owner", 1, time.Hour))
			selection, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "resp_owner", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.EqualValues(t, 1, selection.Account.ID)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}
func TestKeywordRoutingBusyPreferredWaits(t *testing.T) {
	groupID := int64(902)
	on := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}, Extra: map[string]any{KeywordRoutingEnabledExtraKey: true}}
	off := on
	off.ID = 2
	off.Extra = nil
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{on, off}}, cache: &schedulerTestGatewayCache{}, cfg: &config.Config{}, settingService: keywordSettings("hit"), rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("true"), concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false, 2: true}})}
	ctx := WithKeywordRoutingRequest(context.Background(), ContentModerationProtocolOpenAIResponses, []byte(`{"input":"hit"}`), &groupID)
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.EqualValues(t, 1, selection.Account.ID)
	require.False(t, selection.Acquired)
	require.NotNil(t, selection.WaitPlan)
}

type keywordCountingAccountRepo struct {
	schedulerTestOpenAIAccountRepo
	lists int
}

func (r *keywordCountingAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error) {
	r.lists++
	return r.schedulerTestOpenAIAccountRepo.ListSchedulableByGroupIDAndPlatform(ctx, groupID, platform)
}

func TestKeywordRoutingReusesSelectionSnapshotAndRefreshesNextAttempt(t *testing.T) {
	groupID := int64(903)
	on := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2, GroupIDs: []int64{groupID}, Extra: map[string]any{KeywordRoutingEnabledExtraKey: true}}
	off := on
	off.ID, off.Extra = 2, nil
	repo := &keywordCountingAccountRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{on, off}}}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}, settingService: keywordSettings("hit")}
	ctx := WithKeywordRoutingRequest(context.Background(), ContentModerationProtocolOpenAIResponses, []byte(`{"input":"hit"}`), &groupID)
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-5.1", map[int64]struct{}{1: {}}, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, off.ID, selection.Account.ID)
	selection.ReleaseFunc()
	require.Equal(t, 1, repo.lists, "group gate and both pools must share one snapshot")

	// No group-level flag cache survives a selection attempt.
	repo.accounts[0].Extra = nil
	ctx = WithKeywordRoutingRequest(context.Background(), ContentModerationProtocolOpenAIResponses, []byte(`{"input":"hit"}`), &groupID)
	selection, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	selection.ReleaseFunc()
	require.Equal(t, 2, repo.lists)
	require.False(t, keywordRoutingRequestFromContext(ctx).ready, "disabled group must skip extraction and matching")
}

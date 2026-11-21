//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIAdvancedSchedulerUsesCapacityOutsideTopK(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
	accounts := newLegacySchedulerDecisionTestAccounts(38100, true)
	accounts[0].Concurrency = 50
	accounts[1].Concurrency = 50
	svc := newLegacySchedulerDecisionTestService(accounts, true, schedulerTestConcurrencyCache{
		loadMap: map[int64]*AccountLoadInfo{
			38101: {AccountID: 38101},
			38102: {AccountID: 38102, LoadRate: 50, CurrentConcurrency: 25},
		},
		acquireResults: map[int64]bool{38101: false, 38102: true},
	})
	svc.cfg.Gateway.OpenAIWS.LBTopK = 1
	scheduler := newDefaultOpenAIAccountScheduler(svc, newOpenAIAccountRuntimeStats())
	groupID := int64(38100)
	selection, _, err := scheduler.Select(context.Background(), OpenAIAccountScheduleRequest{
		GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.1",
	})
	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Nil(t, selection.WaitPlan)
	require.Equal(t, int64(38102), selection.Account.ID)
	defer selection.ReleaseFunc()
}

func TestOpenAILegacySchedulerUsesHardCapacityAboveLoadFactor(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
	accounts := newLegacySchedulerDecisionTestAccounts(38100, true)
	loadFactor := 10
	for i := range accounts {
		accounts[i].Concurrency = 50
		accounts[i].LoadFactor = &loadFactor
	}
	svc := newLegacySchedulerDecisionTestService(accounts, true, schedulerTestConcurrencyCache{
		loadMap: map[int64]*AccountLoadInfo{
			38101: {AccountID: 38101, LoadRate: 500, CurrentConcurrency: 50},
			38102: {AccountID: 38102, LoadRate: 100, CurrentConcurrency: 10},
		},
		acquireResults: map[int64]bool{38101: false, 38102: true},
	})
	groupID := int64(38100)
	selection, err := svc.selectAccountWithLoadAwareness(context.Background(), &groupID, PlatformOpenAI, "", "gpt-5.1", nil, false, "", false)
	require.NoError(t, err)
	require.True(t, selection.Acquired)
	require.Nil(t, selection.WaitPlan)
	require.Equal(t, int64(38102), selection.Account.ID)
	defer selection.ReleaseFunc()
}

func TestOpenAIAdvancedSchedulerSpreadsSameSessionSpillover(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
	accounts := newLegacySchedulerDecisionTestAccounts(38100, true)
	accounts[1].Priority = accounts[0].Priority
	svc := newLegacySchedulerDecisionTestService(accounts, true, schedulerTestConcurrencyCache{})
	scheduler := newDefaultOpenAIAccountScheduler(svc, newOpenAIAccountRuntimeStats())
	groupID := int64(38100)
	selected := map[int64]int{}
	for i := 0; i < 32; i++ {
		selection, _, err := scheduler.Select(context.Background(), OpenAIAccountScheduleRequest{
			GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.1",
			SessionHash: "same-concurrent-session", PreserveStickyBinding: true,
		})
		require.NoError(t, err)
		require.True(t, selection.Acquired)
		selected[selection.Account.ID]++
		selection.ReleaseFunc()
	}
	require.Len(t, selected, 2, "load-balanced spillovers must not replay one deterministic account order")
}

func TestOpenAIPoolExhaustedRequestsCoolOnlyFailedModel(t *testing.T) {
	svc := &OpenAIGatewayService{}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"pool_mode": true},
	}
	err := &UpstreamFailoverError{StatusCode: http.StatusTooManyRequests, RetryableOnSameAccount: true}
	svc.RecordOpenAIAccountRetryExhausted(account, "gpt-6-luna", err)
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-6-luna"))
	svc.RecordOpenAIAccountRetryExhausted(account, "gpt-6-luna", err)
	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-6-luna"))
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-6-sol"))
	svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-luna", true, nil)
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-6-luna"))
}

func TestOpenAIPoolExhaustionRespectsErrorScopeAndCustomPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    UpstreamFailoverError
		custom bool
	}{
		{name: "provider", err: UpstreamFailoverError{StatusCode: 429, RetryableOnSameAccount: true, Scope: GatewayFailureScopeProvider}},
		{name: "request", err: UpstreamFailoverError{StatusCode: 503, RetryableOnSameAccount: true, RequestScopedTransient: true}},
		{name: "custom_policy", custom: true, err: UpstreamFailoverError{StatusCode: 429, RetryableOnSameAccount: true}},
		{name: "validation", err: UpstreamFailoverError{StatusCode: 400, RetryableOnSameAccount: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"pool_mode": true, "custom_error_codes_enabled": tc.custom},
			}
			for i := 0; i < 3; i++ {
				svc.RecordOpenAIAccountRetryExhausted(account, "gpt-6-luna", &tc.err)
			}
			require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-6-luna"))
		})
	}
}

func BenchmarkOpenAISchedulerCapacityPlan24(b *testing.B) {
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{}, stats: newOpenAIAccountRuntimeStats()}
	accounts := make([]*Account, 24)
	loads := make(map[int64]*AccountLoadInfo, 24)
	for i := range accounts {
		id := int64(i + 1)
		accounts[i] = &Account{ID: id, Concurrency: 50}
		loads[id] = &AccountLoadInfo{AccountID: id, CurrentConcurrency: i % 10, LoadRate: i * 2}
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scheduler.buildOpenAIAccountLoadPlan(ctx, OpenAIAccountScheduleRequest{RequestedModel: "gpt-6-luna", selectionNonce: uint64(i)}, accounts, loads)
	}
}

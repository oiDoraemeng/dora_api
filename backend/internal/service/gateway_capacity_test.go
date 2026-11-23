//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type gatewayCapacityCache struct {
	*mockConcurrencyCache
	limits map[int64]int
}

func (c *gatewayCapacityCache) AcquireAccountSlot(ctx context.Context, id int64, limit int, requestID string) (bool, error) {
	c.limits[id] = limit
	return c.mockConcurrencyCache.AcquireAccountSlot(ctx, id, limit, requestID)
}

func newGatewayCapacityTestService(platform string) (*GatewayService, *gatewayCapacityCache, *mockGatewayCacheForPlatform) {
	soft := 1
	accounts := []Account{
		{ID: 1, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 10, LoadFactor: &soft},
		{ID: 2, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 2, Concurrency: 10, LoadFactor: &soft},
	}
	repo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: map[int64]*Account{}}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	cache := &gatewayCapacityCache{mockConcurrencyCache: &mockConcurrencyCache{
		acquireResults: map[int64]bool{1: false, 2: true},
		loadMap: map[int64]*AccountLoadInfo{
			1: {AccountID: 1, CurrentConcurrency: 10, LoadRate: 1000},
			2: {AccountID: 2, CurrentConcurrency: 1, LoadRate: 100},
		},
	}, limits: map[int64]int{}}
	sticky := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{}}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.Gateway.Scheduling.StickySessionMaxWaiting = 3
	cfg.Gateway.Scheduling.StickySessionWaitTimeout = time.Minute
	cfg.Gateway.Scheduling.FallbackWaitTimeout = 10 * time.Second
	return &GatewayService{accountRepo: repo, cache: sticky, cfg: cfg, concurrencyService: NewConcurrencyService(cache)}, cache, sticky
}

func TestGatewayCapacityUsesHardSlotsAboveSoftLoad(t *testing.T) {
	for _, platform := range []string{PlatformAnthropic, PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			svc, cache, _ := newGatewayCapacityTestService(platform)
			ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, platform)
			result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "", nil, "", 0)
			require.NoError(t, err)
			require.True(t, result.Acquired, "a soft load target must not strand remaining hard capacity")
			require.Equal(t, int64(2), result.Account.ID)
			require.Nil(t, result.WaitPlan)
			require.Equal(t, 10, cache.limits[2], "atomic acquisition must enforce the configured hard limit")
			result.ReleaseFunc()
		})
	}
}

func TestGatewayCapacityBusyKiroStickyUsesAnotherAccountBeforeWaiting(t *testing.T) {
	svc, cache, sticky := newGatewayCapacityTestService(PlatformAnthropic)
	sticky.sessionBindings["busy-session"] = 1
	result, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, "busy-session", "", nil, "", 0)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Equal(t, int64(2), result.Account.ID)
	require.Nil(t, result.WaitPlan)
	require.Equal(t, 10, cache.limits[2])
	require.Equal(t, int64(2), sticky.sessionBindings["busy-session"])
	result.ReleaseFunc()
}

func TestGatewayCapacityRetainsStickyWaitWhenAllSlotsAreBusy(t *testing.T) {
	svc, cache, sticky := newGatewayCapacityTestService(PlatformAnthropic)
	sticky.sessionBindings["busy-session"] = 1
	cache.acquireResults[2] = false
	result, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, "busy-session", "", nil, "", 0)
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Equal(t, int64(1), result.Account.ID)
	require.NotNil(t, result.WaitPlan)
	require.Equal(t, time.Minute, result.WaitPlan.Timeout)
	require.Equal(t, int64(1), sticky.sessionBindings["busy-session"])
}

func TestGatewayCapacityFallbackRespectsEligibility(t *testing.T) {
	for _, gate := range []string{"excluded", "disabled", "cooldown"} {
		t.Run(gate, func(t *testing.T) {
			svc, cache, sticky := newGatewayCapacityTestService(PlatformAnthropic)
			sticky.sessionBindings["busy-session"] = 1
			excluded := map[int64]struct{}{}
			account := svc.accountRepo.(*mockAccountRepoForPlatform).accountsByID[2]
			switch gate {
			case "excluded":
				excluded[2] = struct{}{}
			case "disabled":
				account.Schedulable = false
			case "cooldown":
				until := time.Now().Add(time.Minute)
				account.RateLimitResetAt = &until
			}
			result, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, "busy-session", "", excluded, "", 0)
			require.NoError(t, err)
			require.False(t, result.Acquired)
			require.Equal(t, int64(1), result.Account.ID)
			require.NotContains(t, cache.limits, int64(2))
		})
	}
}

func TestGatewayCapacityHealthyStickyStaysOnAccount(t *testing.T) {
	svc, cache, sticky := newGatewayCapacityTestService(PlatformAnthropic)
	sticky.sessionBindings["healthy-session"] = 1
	cache.acquireResults[1] = true
	result, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, "healthy-session", "", nil, "", 0)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Equal(t, int64(1), result.Account.ID)
	require.Zero(t, cache.loadBatchCalls)
	result.ReleaseFunc()
}

func TestGatewayCapacityKeepsOAuthAndNativeGeminiAffinity(t *testing.T) {
	for _, tc := range []struct {
		platform string
		kind     string
	}{
		{PlatformAnthropic, AccountTypeOAuth},
		{PlatformGemini, AccountTypeAPIKey},
	} {
		t.Run(tc.platform+"_"+tc.kind, func(t *testing.T) {
			svc, cache, sticky := newGatewayCapacityTestService(tc.platform)
			svc.accountRepo.(*mockAccountRepoForPlatform).accountsByID[1].Type = tc.kind
			sticky.sessionBindings["pinned-session"] = 1
			ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, tc.platform)
			result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "pinned-session", "", nil, "", 0)
			require.NoError(t, err)
			require.False(t, result.Acquired)
			require.Equal(t, int64(1), result.Account.ID)
			require.NotNil(t, result.WaitPlan)
			require.NotContains(t, cache.limits, int64(2))
		})
	}
}

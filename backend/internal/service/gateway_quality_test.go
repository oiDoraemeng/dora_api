//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGatewayQualityBalancesLatencySuccessAndCapacity(t *testing.T) {
	s := &GatewayService{}
	fast := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 20}
	slow := &Account{ID: 2, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Concurrency: 20}
	good, bad := false, true
	for i := 0; i < 10; i++ {
		s.qualityStats.observe(fast, "claude-opus", &good, 1000, time.Now())
		s.qualityStats.observe(slow, "claude-opus", &bad, 60000, time.Now())
	}
	items := []accountWithLoad{{slow, &AccountLoadInfo{}}, {fast, &AccountLoadInfo{CurrentConcurrency: 5, LoadRate: 25}}}
	require.Equal(t, fast.ID, s.selectByGatewayQuality(items, "claude-opus", false).account.ID)
	require.True(t, s.gatewayStickyDegraded(slow, "claude-opus"))
	require.False(t, s.gatewayStickyDegraded(slow, "another-model"), "one bad model must not penalize other models")
	require.Equal(t, slow.ID, s.selectByGatewayQuality(items[:1], "claude-opus", false).account.ID, "retain spare hard capacity")
	s.qualityStats.observe(slow, "other", &good, 1000, time.Now())
	s.qualityStats.observe(fast, "other", &good, 1000, time.Now())
	require.Equal(t, slow.ID, s.selectByGatewayQuality(items, "other", false).account.ID)
}

func TestGatewayQualityKeepsPriorityAndEscapesSlowSticky(t *testing.T) {
	s, cache, sticky := newGatewayCapacityTestService(PlatformAnthropic)
	repo := s.accountRepo.(*mockAccountRepoForPlatform)
	repo.accounts[1].Priority = 1
	cache.acquireResults[1], cache.acquireResults[2] = true, true
	cache.loadMap[1] = &AccountLoadInfo{AccountID: 1}
	cache.loadMap[2] = &AccountLoadInfo{AccountID: 2}
	bad := true
	for i := 0; i < 10; i++ {
		s.qualityStats.observe(&repo.accounts[0], "", &bad, 60000, time.Now())
	}
	sticky.sessionBindings["slow"] = 1
	selected, err := s.SelectAccountWithLoadAwareness(context.Background(), nil, "slow", "", nil, "", 0)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID)
	selected.ReleaseFunc()
	repo.accounts[1].Priority = 2
	selected, err = s.SelectAccountWithLoadAwareness(context.Background(), nil, "", "", nil, "", 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.Account.ID)
	selected.ReleaseFunc()
}

func TestGatewayQualityCancellationIsNotFailureAndHistoryAges(t *testing.T) {
	s := &GatewayService{}
	account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.reportGatewayQuality(ctx, account, "m", nil, context.Canceled, 60*time.Second)
	success, latency := s.qualityStats.factors(account, "m", time.Now())
	require.Equal(t, 1.0, success)
	require.Less(t, latency, .15)
	bad := true
	s.qualityStats.observe(account, "old", &bad, 60000, time.Now().Add(-30*time.Minute))
	success, latency = s.qualityStats.factors(account, "old", time.Now())
	require.Greater(t, success, .99)
	require.InDelta(t, .5, latency, .01)
}

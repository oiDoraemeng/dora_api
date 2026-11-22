//go:build unit

package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAISchedulerAgesHistoryWithoutClearingStickyEvidence(t *testing.T) {
	now := time.Now()
	stats := newOpenAIAccountRuntimeStats()
	for range 12 {
		stats.reportAt(1, false, intPtrForTest(60000), now)
	}
	raw, rawTTFT, _ := stats.snapshot(1)
	rate, ttft, hasTTFT, stale := stats.schedulingSnapshot(1, now.Add(3*time.Minute))
	require.InDelta(t, raw, rate, 1e-9)
	require.Equal(t, rawTTFT, ttft)
	require.True(t, hasTTFT)
	require.Zero(t, stale)

	rate, _, hasTTFT, stale = stats.schedulingSnapshot(1, now.Add(8*time.Minute))
	require.InDelta(t, .1+(raw-.1)/2, rate, 1e-9)
	require.True(t, hasTTFT)
	require.InDelta(t, .5, stale, 1e-9)
	rate, _, hasTTFT, _ = stats.schedulingSnapshot(1, now.Add(30*time.Minute))
	require.Less(t, rate, .13)
	require.False(t, hasTTFT)
	current, currentTTFT, _ := stats.snapshot(1)
	require.Equal(t, raw, current, "aging must not manufacture a successful result")
	require.Equal(t, rawTTFT, currentTTFT)

	stats.reportAt(1, true, intPtrForTest(1000), now.Add(time.Hour))
	rate, ttft, hasTTFT, stale = stats.schedulingSnapshot(1, now.Add(time.Hour))
	require.Less(t, rate, .081, "new success must not resurrect old failures")
	require.Less(t, ttft, 1050.0, "new latency must supersede stale TTFT")
	require.True(t, hasTTFT)
	require.Zero(t, stale)
}

func TestOpenAISchedulerFailedResultsDoNotRefreshOldTTFT(t *testing.T) {
	now := time.Now()
	stats := newOpenAIAccountRuntimeStats()
	stats.reportAt(1, true, intPtrForTest(30000), now)
	stats.reportAt(1, false, nil, now.Add(31*time.Minute))
	rate, _, hasTTFT, _ := stats.schedulingSnapshot(1, now.Add(31*time.Minute))
	require.Greater(t, rate, .2)
	require.False(t, hasTTFT)
}

func recoveryTestCandidates(now time.Time) []openAIAccountCandidateScore {
	lastUsed := now.Add(-time.Hour)
	out := make([]openAIAccountCandidateScore, 4)
	for i := range out {
		out[i] = openAIAccountCandidateScore{
			account: &Account{ID: int64(i + 1), Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 20, LastUsedAt: &lastUsed},
			loadInfo: &AccountLoadInfo{}, loadKnown: true, score: float64(4 - i),
		}
	}
	return out
}

func recoveryTestRequest() OpenAIAccountScheduleRequest {
	return OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: "gpt-6-luna", selectionNonce: 20}
}

func TestOpenAISchedulerRecoveryIsBoundedAndKeepsPreferredPool(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{stats: newOpenAIAccountRuntimeStats()}
	for nonce := uint64(1); nonce <= 40; nonce++ {
		req := recoveryTestRequest()
		req.selectionNonce = nonce
		order := scheduler.promoteOpenAIRecoveryCandidate(req, recoveryTestCandidates(time.Now()), 2)
		if nonce%20 == 0 {
			require.True(t, order[0].recovery)
			require.Greater(t, order[0].account.ID, int64(2))
			require.Equal(t, int64(1), order[1].account.ID)
			require.Equal(t, int64(2), order[2].account.ID)
		} else {
			require.False(t, order[0].recovery)
			require.Equal(t, int64(1), order[0].account.ID)
		}
	}
}

func TestOpenAISchedulerRecoveryPreservesOwnersAndMaintenance(t *testing.T) {
	tests := []struct {
		name   string
		change func(*OpenAIAccountScheduleRequest)
	}{
		{"compact", func(r *OpenAIAccountScheduleRequest) { r.RequireCompact = true }},
		{"previous_response", func(r *OpenAIAccountScheduleRequest) { r.PreviousResponseID = "resp_owner" }},
		{"guardian", func(r *OpenAIAccountScheduleRequest) { r.GuardianParentAccountID = 1 }},
		{"pinned", func(r *OpenAIAccountScheduleRequest) { r.DisableStickyEscape = true }},
		{"weighted_sticky", func(r *OpenAIAccountScheduleRequest) { r.StickyWeighted = true }},
		{"image", func(r *OpenAIAccountScheduleRequest) { r.RequiredImageCapability = OpenAIImagesCapabilityNative }},
		{"live", func(r *OpenAIAccountScheduleRequest) { r.RequiredCapability = OpenAIEndpointCapabilityLive }},
		{"grok", func(r *OpenAIAccountScheduleRequest) { r.Platform = PlatformGrok }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &defaultOpenAIAccountScheduler{stats: newOpenAIAccountRuntimeStats()}
			req := recoveryTestRequest()
			tc.change(&req)
			order := s.promoteOpenAIRecoveryCandidate(req, recoveryTestCandidates(time.Now()), 2)
			require.Equal(t, int64(1), order[0].account.ID)
			require.False(t, order[0].recovery)
		})
	}
}

func TestOpenAISchedulerRecoverySkipsBusyUnknownAndRecentAccounts(t *testing.T) {
	now := time.Now()
	s := &defaultOpenAIAccountScheduler{stats: newOpenAIAccountRuntimeStats()}
	cases := []struct {
		name   string
		change func(*openAIAccountCandidateScore)
	}{
		{"busy", func(c *openAIAccountCandidateScore) { c.loadInfo.CurrentConcurrency = 1 }},
		{"waiting", func(c *openAIAccountCandidateScore) { c.loadInfo.WaitingCount = 1 }},
		{"unknown_load", func(c *openAIAccountCandidateScore) { c.loadKnown = false }},
		{"lower_priority", func(c *openAIAccountCandidateScore) { c.account.Priority = 1 }},
		{"recent", func(c *openAIAccountCandidateScore) { c.account.LastUsedAt = &now }},
		{"oauth", func(c *openAIAccountCandidateScore) { c.account.Type = AccountTypeOAuth }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			order := recoveryTestCandidates(now)[:3]
			tc.change(&order[2])
			order = s.promoteOpenAIRecoveryCandidate(recoveryTestRequest(), order, 2)
			require.False(t, order[0].recovery)
		})
	}
	s.stats.markSelected(3, now)
	order := s.promoteOpenAIRecoveryCandidate(recoveryTestRequest(), recoveryTestCandidates(now)[:3], 2)
	require.False(t, order[0].recovery)
}

func TestOpenAISchedulerRecoveryReservationSurvivesLongRequests(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	now := time.Now()
	var wins atomic.Int64
	var wg sync.WaitGroup
	var winner func()
	for range 64 {
		wg.Go(func() {
			if release, ok := stats.beginRecovery(1, now); ok {
				wins.Add(1)
				winner = release
			}
		})
	}
	wg.Wait()
	require.Equal(t, int64(1), wins.Load())
	_, ok := stats.beginRecovery(1, now.Add(20*time.Minute))
	require.False(t, ok, "an unfinished recovery must not multiply after its idle interval")
	winner()
	_, ok = stats.beginRecovery(1, now.Add(time.Minute))
	require.False(t, ok)
	release, ok := stats.beginRecovery(1, now.Add(4*time.Minute))
	require.True(t, ok)
	release()
}

func TestOpenAISchedulerRecoveryRechecksHealthAndDoesNotRebindSession(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	accounts := newLegacySchedulerDecisionTestAccounts(38100, true)
	accounts[1].Priority = accounts[0].Priority
	for i := range accounts {
		accounts[i].Concurrency = 20
	}
	svc := newLegacySchedulerDecisionTestService(accounts, true, schedulerTestConcurrencyCache{})
	s := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
	order := []openAIAccountCandidateScore{{account: &accounts[1], loadInfo: &AccountLoadInfo{}, loadKnown: true, recovery: true}, {account: &accounts[0], loadInfo: &AccountLoadInfo{}, loadKnown: true}}
	req := recoveryTestRequest()
	req.SessionHash = "keep-existing-binding"
	selection, _, err := s.tryAcquireOpenAISelectionOrder(context.Background(), req, order)
	require.NoError(t, err)
	require.Equal(t, accounts[1].ID, selection.Account.ID)
	cache := svc.cache.(*schedulerTestGatewayCache)
	require.Empty(t, cache.sessionBindings)
	stat := s.stats.loadOrCreate(accounts[1].ID)
	require.True(t, stat.recoveryInFlight.Load())
	selection.ReleaseFunc()
	selection.ReleaseFunc()
	require.False(t, stat.recoveryInFlight.Load())

	// A disabled candidate from a stale plan must still fail the normal fresh
	// eligibility check and leave the preferred account available.
	accounts[1].Schedulable = false
	s.stats.loadOrCreate(accounts[1].ID).recoveryAt.Store(0)
	selection, _, err = s.tryAcquireOpenAISelectionOrder(context.Background(), req, order)
	require.NoError(t, err)
	require.Equal(t, accounts[0].ID, selection.Account.ID)
	selection.ReleaseFunc()
	require.False(t, stat.recoveryInFlight.Load())
}

func TestOpenAISchedulerRecoveryReachesIdleAccountWhilePrimaryHasCapacity(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	accounts := newLegacySchedulerDecisionTestAccounts(38100, true)
	accounts[1].Priority = accounts[0].Priority
	for i := range accounts {
		accounts[i].Concurrency = 20
	}
	svc := newLegacySchedulerDecisionTestService(accounts, true, schedulerTestConcurrencyCache{})
	svc.cfg.Gateway.OpenAIWS.LBTopK = 1
	stats := newOpenAIAccountRuntimeStats()
	stats.report(accounts[0].ID, true, intPtrForTest(100))
	for range 12 {
		stats.reportAt(accounts[1].ID, false, intPtrForTest(60000), time.Now().Add(-4*time.Minute))
	}
	s := &defaultOpenAIAccountScheduler{service: svc, stats: stats}
	groupID := int64(38100)
	for n := 1; n <= 40; n++ {
		selection, _, err := s.Select(context.Background(), OpenAIAccountScheduleRequest{
			GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-5.1",
		})
		require.NoError(t, err)
		want := accounts[0].ID
		if n == 20 {
			want = accounts[1].ID
		}
		require.Equal(t, want, selection.Account.ID, "selection %d", n)
		selection.ReleaseFunc()
	}
}

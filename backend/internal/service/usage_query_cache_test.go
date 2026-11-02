//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestUsageQueryCacheSharesWorkAcrossCanceledRequests(t *testing.T) {
	cache := newUsageQueryCache()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	loader := func(ctx context.Context) (any, error) {
		calls.Add(1)
		close(started)
		<-release
		return []int{7}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		var value []int
		first <- cache.load(ctx, "user:1", &value, loader)
	}()
	<-started
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var value []int
			err := cache.load(context.Background(), "user:1", &value, loader)
			if err != nil || len(value) != 1 || value[0] != 7 {
				t.Errorf("unexpected cache result: %v, %v", value, err)
			}
		}()
	}
	close(release)
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())
}

func TestUsageQueryCacheExpiryErrorsAndSnapshots(t *testing.T) {
	cache := newUsageQueryCache()
	var calls int
	loader := func(context.Context) (any, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary database error")
		}
		return map[int64][]int{1: {calls}}, nil
	}
	var value map[int64][]int
	require.Error(t, cache.load(context.Background(), "one", &value, loader))
	require.NoError(t, cache.load(context.Background(), "one", &value, loader))
	value[1][0] = 999
	require.NoError(t, cache.load(context.Background(), "one", &value, loader))
	require.Equal(t, 2, value[1][0])
	require.Equal(t, 2, calls)
	cache.mu.Lock()
	item := cache.items["one"]
	item.expiresAt = time.Now().Add(-time.Second)
	cache.items["one"] = item
	cache.mu.Unlock()
	require.NoError(t, cache.load(context.Background(), "one", &value, loader))
	require.Equal(t, 3, value[1][0])
	cache.maxEntries = 2
	for _, key := range []string{"two", "three", "four"} {
		require.NoError(t, cache.load(context.Background(), key, &value, loader))
	}
	require.Len(t, cache.items, 2)
}

type cachedDashboardRepo struct {
	UsageLogRepository
	calls int
}

func (r *cachedDashboardRepo) GetUserDashboardStats(_ context.Context, userID int64) (*usagestats.UserDashboardStats, error) {
	r.calls++
	return &usagestats.UserDashboardStats{TotalRequests: userID}, nil
}

func TestUsageServiceDashboardCacheIsUserAndInstanceScoped(t *testing.T) {
	repo := &cachedDashboardRepo{}
	svc := NewUsageService(repo, nil, nil, nil)
	for _, userID := range []int64{1, 2, 1} {
		stats, err := svc.GetUserDashboardStats(context.Background(), userID)
		require.NoError(t, err)
		require.Equal(t, userID, stats.TotalRequests)
		stats.TotalRequests = 999
	}
	require.Equal(t, 2, repo.calls)
	other := NewUsageService(repo, nil, nil, nil)
	_, err := other.GetUserDashboardStats(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 3, repo.calls)
}

func TestUsageQueryCacheKeyIncludesFilterValues(t *testing.T) {
	flag := false
	first := usagestats.UsageLogFilters{UserID: 1, Stream: &flag}
	key := usageQueryCacheKey("trend", first)
	otherFlag := false
	first.Stream = &otherFlag
	require.Equal(t, key, usageQueryCacheKey("trend", first))
	otherFlag = true
	require.NotEqual(t, key, usageQueryCacheKey("trend", first))
	first.UserID = 2
	require.NotEqual(t, key, usageQueryCacheKey("trend", first))
}

type cachedFiltersRepo struct {
	UsageLogRepository
	trendCalls, modelCalls, keyCalls int
}

func (r *cachedFiltersRepo) GetUsageTrendWithUsageFilters(_ context.Context, _, _ time.Time, _ string, filters usagestats.UsageLogFilters) ([]usagestats.TrendDataPoint, error) {
	r.trendCalls++
	return []usagestats.TrendDataPoint{{Requests: filters.UserID}}, nil
}

func (r *cachedFiltersRepo) GetModelStatsWithUsageFiltersBySource(_ context.Context, _, _ time.Time, filters usagestats.UsageLogFilters, source string) ([]usagestats.ModelStat, error) {
	r.modelCalls++
	return []usagestats.ModelStat{{Model: source, Requests: filters.UserID}}, nil
}

func (r *cachedFiltersRepo) GetBatchAPIKeyUsageStats(_ context.Context, ids []int64, _, _ time.Time) (map[int64]*usagestats.BatchAPIKeyUsageStats, error) {
	r.keyCalls++
	stats := make(map[int64]*usagestats.BatchAPIKeyUsageStats)
	for _, id := range ids {
		stats[id] = &usagestats.BatchAPIKeyUsageStats{APIKeyID: id, TotalActualCost: float64(id)}
	}
	return stats, nil
}

func TestUsageServiceFilteredCacheSeparatesUsersDatesAndSources(t *testing.T) {
	repo := &cachedFiltersRepo{}
	svc := NewUsageService(repo, nil, nil, nil)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	for _, id := range []int64{1, 2, 1} {
		filters := usagestats.UsageLogFilters{UserID: id}
		trend, err := svc.GetUsageTrendWithFilters(context.Background(), start, end, "day", filters)
		require.NoError(t, err)
		require.Equal(t, id, trend[0].Requests)
		models, err := svc.GetModelStatsWithFiltersBySource(context.Background(), start, end, filters, "requested")
		require.NoError(t, err)
		require.Equal(t, id, models[0].Requests)
	}
	require.Equal(t, 2, repo.trendCalls)
	require.Equal(t, 2, repo.modelCalls)
	_, err := svc.GetUsageTrendWithFilters(context.Background(), start, end.AddDate(0, 0, 1), "day", usagestats.UsageLogFilters{UserID: 1})
	require.NoError(t, err)
	_, err = svc.GetModelStatsWithFiltersBySource(context.Background(), start, end, usagestats.UsageLogFilters{UserID: 1}, "upstream")
	require.NoError(t, err)
	require.Equal(t, 3, repo.trendCalls)
	require.Equal(t, 3, repo.modelCalls)
	for _, ids := range [][]int64{{1, 2}, {2, 1}, {3}} {
		stats, err := svc.GetBatchAPIKeyUsageStats(context.Background(), ids, time.Time{}, time.Time{})
		require.NoError(t, err)
		for _, id := range ids {
			require.Equal(t, float64(id), stats[id].TotalActualCost)
		}
	}
	require.Equal(t, 2, repo.keyCalls)
}

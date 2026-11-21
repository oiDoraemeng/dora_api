package repository

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestKeywordRoutingStatsUnresponsiveRedisIsBounded(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()
	client := redis.NewClient(&redis.Options{
		Addr: listener.Addr().String(), MaxRetries: -1,
		ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	})
	t.Cleanup(func() { _ = client.Close() })
	store := NewKeywordRoutingStatsCache(client)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.Error(t, store.Record(ctx, timezone.Now(), 1, []string{"test"}))
	require.Less(t, time.Since(start), time.Second)
}

func TestKeywordRoutingStatsAtomicDimensionsAndExpiry(t *testing.T) {
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewKeywordRoutingStatsCache(client)
	now := timezone.Now()
	mini.SetTime(now)
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			words := []string(nil)
			if i%2 == 0 {
				words = []string{"画图", "图"}
			}
			errs <- store.Record(context.Background(), now, int64(i%2+1), words)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	stats, err := store.Today(context.Background(), nil)
	require.NoError(t, err)
	require.EqualValues(t, 24, stats.Checks)
	require.EqualValues(t, 12, stats.Hits)
	require.Len(t, stats.Groups, 2)
	require.Len(t, stats.Keywords, 2)
	require.EqualValues(t, 12, stats.Keywords[0].Hits)
	group := int64(2)
	stats, err = store.Today(context.Background(), &group)
	require.NoError(t, err)
	require.EqualValues(t, 12, stats.Checks)
	require.Zero(t, stats.Hits)
	require.Empty(t, stats.Keywords)
	key := keywordRoutingStatsKey(now)
	ttl := mini.TTL(key)
	require.InDelta(t, timezone.StartOfDay(now).AddDate(0, 0, 1).Sub(now).Seconds(), ttl.Seconds(), 2)
	mini.FastForward(ttl + time.Second)
	require.False(t, mini.Exists(key))
	mini.SetTime(timezone.StartOfDay(now).AddDate(0, 0, 1))
	require.NoError(t, store.Record(context.Background(), now, 1, []string{"late"}))
	require.False(t, mini.Exists(key))
}
func TestKeywordRoutingSchedulerProjection(t *testing.T) {
	require.Equal(t, true, filterSchedulerExtra(map[string]any{"keyword_routing_enabled": true, "unrelated": "x"})["keyword_routing_enabled"])
	require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{"keyword_routing_enabled": true}))
}

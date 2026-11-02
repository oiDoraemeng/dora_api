package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// usageQueryCache is deliberately local to a UsageService instance. It keeps
// dashboard data bounded and short lived, while singleflight prevents a cold
// dashboard from issuing the same expensive aggregate once per browser tab.
// Billing and authorization paths do not use this cache.
type usageQueryCache struct {
	mu         sync.Mutex
	items      map[string]usageQueryCacheItem
	maxEntries int
	ttl        time.Duration
	flight     singleflight.Group
}

type usageQueryCacheItem struct {
	value     []byte
	expiresAt time.Time
}

func newUsageQueryCache() *usageQueryCache {
	return &usageQueryCache{
		items:      make(map[string]usageQueryCacheItem),
		maxEntries: 256,
		ttl:        30 * time.Second,
	}
}

// load unmarshals a snapshot into target. Values are serialized before being
// stored so callers cannot mutate a cached pointer or slice in place.
func (c *usageQueryCache) load(ctx context.Context, key string, target any, fn func(context.Context) (any, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || key == "" {
		value, err := fn(ctx)
		if err != nil {
			return err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, target)
	}

	if data, ok := c.get(key); ok {
		return json.Unmarshal(data, target)
	}

	// A request that has already been canceled should not start a database
	// query, but an in-flight query is allowed to finish for other waiters.
	resultCh := c.flight.DoChan(key, func() (any, error) {
		if data, ok := c.get(key); ok {
			return data, nil
		}
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		value, err := fn(loadCtx)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		c.put(key, data)
		return data, nil
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return result.Err
		}
		return json.Unmarshal(result.Val.([]byte), target)
	}
}

func (c *usageQueryCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(item.expiresAt) {
		delete(c.items, key)
		return nil, false
	}
	return append([]byte(nil), item.value...), true
}

func (c *usageQueryCache) put(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.maxEntries {
		// Entries are short lived, so removing the oldest expiry is sufficient
		// and avoids maintaining a second LRU data structure.
		var oldestKey string
		var oldest time.Time
		for k, item := range c.items {
			if oldestKey == "" || item.expiresAt.Before(oldest) {
				oldestKey, oldest = k, item.expiresAt
			}
		}
		if oldestKey != "" {
			delete(c.items, oldestKey)
		}
	}
	c.items[key] = usageQueryCacheItem{value: append([]byte(nil), value...), expiresAt: time.Now().Add(c.ttl)}
}

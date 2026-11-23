// Package upstreamctx keeps cancellation prompt before response headers while
// allowing a bounded usage drain after an upstream response has started.
package upstreamctx

import (
	"context"
	"sync"
	"time"
)

type clientKey struct{}

func Detached(parent context.Context) context.Context {
	if parent == nil {
		return context.Background()
	}
	client := parent
	if original, ok := parent.Value(clientKey{}).(context.Context); ok {
		client = original
	}
	return context.WithValue(context.WithoutCancel(parent), clientKey{}, client)
}

// Attempt owns only this HTTP attempt. accepted must be called when headers
// arrive; release must run when the body closes or the attempt fails.
func Attempt(base context.Context, drain time.Duration) (ctx context.Context, accepted, release func()) {
	ctx, cancel := context.WithCancel(base)
	parent, ok := base.Value(clientKey{}).(context.Context)
	if !ok || parent.Done() == nil {
		return ctx, func() {}, cancel
	}
	var mu sync.Mutex
	var timer *time.Timer
	started, closed := false, false
	stop := context.AfterFunc(parent, func() {
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return
		}
		if !started || drain <= 0 {
			cancel()
		} else {
			timer = time.AfterFunc(drain, cancel)
		}
	})
	if parent.Err() != nil {
		cancel()
	}
	accepted = func() {
		mu.Lock()
		started = true
		mu.Unlock()
	}
	release = func() {
		mu.Lock()
		closed = true
		stop()
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
		cancel()
	}
	return
}

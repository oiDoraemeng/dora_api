package upstreamctx

import (
	"context"
	"testing"
	"time"
)

func TestCancelBeforeHeaders(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx, _, release := Attempt(Detached(Detached(parent)), time.Second)
	defer release()
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("waiting upstream did not cancel with client")
	}
}

func TestCancelAfterHeadersHasBoundedDrain(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx, accepted, release := Attempt(Detached(parent), 40*time.Millisecond)
	defer release()
	accepted()
	cancel()
	if ctx.Err() != nil {
		t.Fatal("response must have time to collect final usage")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("disconnected stream kept draining indefinitely")
	}
}

func TestAttemptReleaseDoesNotCancelRetry(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := Detached(parent)
	first, _, release := Attempt(base, time.Second)
	release()
	release()
	if first.Err() == nil || base.Err() != nil || parent.Err() != nil {
		t.Fatal("release must only cancel its own HTTP attempt")
	}
	second, _, done := Attempt(base, time.Second)
	defer done()
	if second.Err() != nil {
		t.Fatal("retry inherited cancellation from previous attempt")
	}
}

package nativeapi

import (
	"context"
	"time"
)

const (
	busyEvery = 250 * time.Millisecond
	// busyMost bounds how long a re-register waits, so a turn that never ends cannot keep new
	// entities from Home Assistant for good.
	busyMost = 2 * time.Minute
)

// waitIdle waits while busy reports a turn or announcement under way, checking every so often,
// for at most a bounded time or until ctx ends. It reports whether it waited at all.
func waitIdle(ctx context.Context, busy func() bool, every, most time.Duration) bool {
	if busy == nil || !busy() {
		return false
	}
	t := time.NewTicker(every)
	defer t.Stop()
	give := time.NewTimer(most)
	defer give.Stop()
	for {
		select {
		case <-ctx.Done():
			return true
		case <-give.C:
			return true
		case <-t.C:
			if !busy() {
				return true
			}
		}
	}
}

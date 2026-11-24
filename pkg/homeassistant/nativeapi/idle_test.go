package nativeapi

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnIdleDeviceReRegistersAtOnce(t *testing.T) {
	if waitIdle(context.Background(), func() bool { return false }, time.Hour, time.Hour) {
		t.Fatal("waited with nothing under way")
	}
	if waitIdle(context.Background(), nil, time.Hour, time.Hour) {
		t.Fatal("waited with no way to tell")
	}
}

func TestAReRegisterWaitsForTheTurnToEnd(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	done := make(chan bool, 1)
	go func() {
		done <- waitIdle(context.Background(), busy.Load, time.Millisecond, time.Hour)
	}()

	select {
	case <-done:
		t.Fatal("re-registered during the turn")
	case <-time.After(20 * time.Millisecond):
	}
	busy.Store(false)

	if !<-done {
		t.Fatal("did not report the wait")
	}
}

func TestAReRegisterDoesNotWaitForeverOnATurnThatNeverEnds(t *testing.T) {
	done := make(chan bool, 1)
	go func() {
		done <- waitIdle(context.Background(), func() bool { return true },
			time.Millisecond, 20*time.Millisecond)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting past the bound")
	}
}

func TestAReRegisterStopsWaitingWhenTheServerStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if !waitIdle(ctx, func() bool { return true }, time.Hour, time.Hour) {
		t.Fatal("did not return when the server stopped")
	}
}

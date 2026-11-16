package service

import (
	"context"
	"testing"
	"time"
)

// stuck ignores its context until it is let go, like a service blocked in a read nothing wakes.
func stuck(release <-chan struct{}) func(context.Context) error {
	return func(context.Context) error {
		<-release
		return nil
	}
}

func runGroup(g *Group) (cancel func(), done <-chan error) {
	ctx, stop := context.WithCancel(context.Background())
	ended := make(chan error, 1)
	go func() { ended <- g.Run(ctx) }()
	return stop, ended
}

func TestAGroupStopsByItsDeadlineWhenAServiceWillNot(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	fine := &fake{name: "fine"}
	wedged := &fake{name: "wedged", run: stuck(release)}

	g := New()
	g.StopWithin(50 * time.Millisecond)
	g.Add(fine)
	g.Add(wedged)
	cancel, done := runGroup(g)
	waitFor(t, func() bool { _, runs, _ := wedged.counts(); return runs == 1 })
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the group waited past its deadline for a service that would not stop")
	}
	if _, _, closes := fine.counts(); closes != 1 {
		t.Errorf("the service that stopped was closed %d times, want 1", closes)
	}
	if _, _, closes := wedged.counts(); closes != 0 {
		t.Errorf("the service still running was closed %d times, want 0", closes)
	}
}

func TestWithoutADeadlineAGroupWaitsForEveryService(t *testing.T) {
	release := make(chan struct{})
	wedged := &fake{name: "wedged", run: stuck(release)}

	g := New()
	g.Add(wedged)
	cancel, done := runGroup(g)
	waitFor(t, func() bool { _, runs, _ := wedged.counts(); return runs == 1 })
	cancel()

	select {
	case <-done:
		t.Fatal("the group stopped while a service was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the group did not stop once the service did")
	}
	if _, _, closes := wedged.counts(); closes != 1 {
		t.Errorf("the service was closed %d times, want 1", closes)
	}
}

func TestADeadlineDoesNotHurryAGroupThatStopsInTime(t *testing.T) {
	a, b := &fake{name: "a"}, &fake{name: "b"}
	g := New()
	g.StopWithin(time.Minute)
	g.Add(a)
	g.Add(b)
	cancel, done := runGroup(g)
	waitFor(t, func() bool { _, runs, _ := b.counts(); return runs == 1 })
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a group whose services all stopped did not stop")
	}
	for _, f := range []*fake{a, b} {
		if _, _, closes := f.counts(); closes != 1 {
			t.Errorf("%s was closed %d times, want 1", f.name, closes)
		}
	}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

type Group struct {
	mu       sync.Mutex
	entries  []*entry
	started  bool
	stopping bool
	within   time.Duration
}

type entry struct {
	svc    Service
	policy policy

	mu     sync.Mutex
	state  State
	err    error
	since  time.Time
	starts int

	// supervised and over bracket the entry's supervisor: started, and returned.
	supervised, over atomic.Bool
}

// running reports whether the entry's supervisor, and so perhaps its Run, has not returned.
func (e *entry) running() bool { return e.supervised.Load() && !e.over.Load() }

func New() *Group { return &Group{} }

// StopWithin bounds how long stopping waits for the services to return. A service still running
// at the deadline is named in the log and left behind, unclosed, and the group stops anyway. Zero,
// the default, waits for every service however long it takes.
func (g *Group) StopWithin(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.within = d
}

func (g *Group) Add(svc Service, opts ...Option) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.started {
		slog.Error("service added after the group started", "service", svc.Name())
		return
	}

	p := defaults()
	for _, o := range opts {
		o(&p)
	}
	g.entries = append(
		g.entries,
		&entry{svc: svc, policy: p, state: StateWaiting, since: time.Now()},
	)
}

func (g *Group) Status() []Status {
	g.mu.Lock()
	entries := make([]*entry, len(g.entries))
	copy(entries, g.entries)
	g.mu.Unlock()

	out := make([]Status, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.status())
	}
	return out
}

func (g *Group) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g.mu.Lock()
	if g.started {
		g.mu.Unlock()
		return errors.New("service: group already running")
	}
	g.started = true
	entries := make([]*entry, len(g.entries))
	copy(entries, g.entries)
	g.mu.Unlock()

	var (
		wg      sync.WaitGroup
		started []*entry
	)

	for _, e := range entries {
		if ctx.Err() != nil {
			g.stop(started, &wg)
			return nil
		}
		began := time.Now()
		acquired := false

		if err := e.start(ctx); err != nil {
			if ctx.Err() != nil {
				started = append(started, e)
				g.stop(started, &wg)
				return nil
			}
			e.set(StateFailed, err)
			if e.policy.required {
				slog.Error(
					"required service unavailable, giving up",
					"service",
					e.svc.Name(),
					"err",
					err,
				)
				cancel()
				started = append(started, e)
				g.stop(started, &wg)
				return fmt.Errorf("service: %s: %w", e.svc.Name(), err)
			}
			if !e.policy.restart {
				e.close()
				slog.Error("service unavailable, continuing without it",
					"service", e.svc.Name(), "err", err)
				continue
			}

			slog.Error("service unavailable, still asking for it",
				"service", e.svc.Name(), "err", err)
		} else {
			slog.Debug("service started", "service", e.svc.Name(),
				"in", time.Since(began).Round(time.Millisecond))
			acquired = true
		}

		started = append(started, e)

		wg.Add(1)
		e.supervised.Store(true)
		safe.Go("service "+e.svc.Name(), func() {
			defer wg.Done()
			defer e.over.Store(true)
			g.supervise(ctx, e, acquired)
		})
	}

	<-ctx.Done()
	g.stop(started, &wg)
	return nil
}

func (g *Group) supervise(ctx context.Context, e *entry, acquired bool) {
	wait := e.policy.backoff

	if !acquired {
		if !g.reacquire(ctx, e, &wait) {
			return
		}
		slog.Info("service acquired", "service", e.svc.Name())
	}

	for {
		e.set(StateRunning, nil)
		began := time.Now()

		err := run(ctx, e.svc.Name(), e.svc.Run)
		ran := time.Since(began)

		if ctx.Err() != nil {
			e.set(StateStopped, nil)
			return
		}
		if err == nil {
			e.set(StateStopped, nil)
			slog.Debug(
				"service finished",
				"service",
				e.svc.Name(),
				"ran",
				ran.Round(time.Millisecond),
			)
			return
		}
		if !e.policy.restart || isPermanent(err) {
			e.set(StateFailed, err)
			slog.Error("service failed", "service", e.svc.Name(), "err", err)
			return
		}

		if ran >= e.policy.steady {
			wait = e.policy.backoff
		}

		e.set(StateRetrying, err)
		slog.Error("service failed, restarting", "service", e.svc.Name(),
			"err", err, "ran", ran.Round(time.Millisecond), "in", wait)

		if !g.reacquire(ctx, e, &wait) {
			return
		}
		e.countRestart()
		slog.Info("service restarted", "service", e.svc.Name(), "restarts", e.status().Restarts)
	}
}

func (g *Group) reacquire(ctx context.Context, e *entry, wait *time.Duration) bool {
	for {
		select {
		case <-ctx.Done():
			e.set(StateStopped, nil)
			return false
		case <-time.After(*wait):
		}
		*wait = grow(*wait, e.policy.maxBackoff)

		e.release()

		err := e.start(ctx)
		if err == nil {
			return true
		}

		e.set(StateRetrying, err)
		slog.Error(
			"service could not be reacquired",
			"service",
			e.svc.Name(),
			"err",
			err,
			"in",
			*wait,
		)
	}
}

func grow(wait, max time.Duration) time.Duration {
	if next := wait * 2; next <= max {
		return next
	}
	return max
}

func (g *Group) stop(started []*entry, wg *sync.WaitGroup) {
	g.mu.Lock()
	g.stopping = true
	within := g.within
	g.mu.Unlock()

	if !wait(wg, within) {
		var running []string
		for _, e := range started {
			if e.running() {
				running = append(running, e.svc.Name())
			}
		}
		slog.Error("services did not stop in time, stopping without them",
			"services", running, "after", within)
	}
	for i := len(started) - 1; i >= 0; i-- {
		if !started[i].running() {
			started[i].close()
		}
	}
}

// wait reports whether wg finished within d; zero waits for as long as it takes.
func wait(wg *sync.WaitGroup, d time.Duration) bool {
	if d <= 0 {
		wg.Wait()
		return true
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

func run(ctx context.Context, name string, f func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recovered from a panic",
				"in", "service "+name, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return f(ctx)
}

func (e *entry) start(ctx context.Context) error {
	if s, ok := e.svc.(Starter); ok {
		return s.Start(ctx)
	}
	return nil
}

func (e *entry) close() {
	c, ok := e.svc.(Closer)
	if !ok {
		return
	}
	if err := c.Close(); err != nil {
		slog.Error("closing a service failed", "service", e.svc.Name(), "err", err)
	}
}

func (e *entry) release() {
	r, ok := e.svc.(Reacquirer)
	if !ok {
		e.close()
		return
	}
	if err := r.Release(); err != nil {
		slog.Error("releasing a service failed", "service", e.svc.Name(), "err", err)
	}
}

func (e *entry) set(s State, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state, e.err, e.since = s, err, time.Now()
}

func (e *entry) countRestart() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.starts++
}

func (e *entry) status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Status{
		Name:     e.svc.Name(),
		State:    e.state,
		Err:      e.err,
		Restarts: e.starts,
		Since:    e.since,
	}
}

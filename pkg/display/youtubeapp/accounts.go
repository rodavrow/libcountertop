package youtubeapp

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/ygelfand/libcountertop/pkg/media/youtube"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type signing struct {
	code    youtube.Code
	asking  bool
	problem string
	cancel  context.CancelFunc
}

type accounts struct {
	o Options

	mu        sync.Mutex
	sessions  map[string]*youtube.Session
	signing   *signing
	visitor   *youtube.Visitor
	anonymous *youtube.Session
}

func newAccounts(o Options) *accounts {
	return &accounts{o: o, sessions: map[string]*youtube.Session{}}
}

func (a *accounts) list() []schema.YouTubeAccount { return a.o.Accounts.Read().Accounts }

func (a *accounts) current() (schema.YouTubeAccount, bool) { return a.o.Accounts.Read().Current() }

func (a *accounts) isAnonymous() bool { return a.o.Accounts.Read().Anonymous() }

func (a *accounts) stayAnonymous() { a.choose(schema.YouTubeAnonymous) }

func (a *accounts) profile() *youtube.Visitor {
	if !a.isAnonymous() {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.visitorLocked()
}

func (a *accounts) chosen() *youtube.Session {
	acc, ok := a.current()
	if !ok {
		return nil
	}
	return a.session(acc)
}

func (a *accounts) anonymousSession() *youtube.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.anonymous == nil {
		a.anonymous = youtube.Anonymous(a.o.Client, a.visitorLocked())
	}
	return a.anonymous
}

func (a *accounts) visitorLocked() *youtube.Visitor {
	if a.visitor == nil {
		a.visitor = youtube.NewVisitor(a.o.Accounts.Read().Visitor, a.keepVisitor)
	}
	return a.visitor
}

func (a *accounts) keepVisitor(id string) {
	if err := a.o.Accounts.Update(func(y *schema.YouTubeAccounts) { y.Visitor = id }); err != nil {
		slog.Warn("youtube accounts", "err", err)
	}
}

func (a *accounts) reset() {
	a.mu.Lock()
	a.visitor, a.anonymous = nil, nil
	a.mu.Unlock()
	if err := a.o.Accounts.Update(func(y *schema.YouTubeAccounts) { y.Visitor = "" }); err != nil {
		slog.Warn("youtube accounts", "err", err)
	}
}

func (a *accounts) choose(id string) {
	if err := a.o.Accounts.Update(func(y *schema.YouTubeAccounts) { y.Chosen = id }); err != nil {
		slog.Warn("youtube accounts", "err", err)
	}
}

func (a *accounts) remove(id string) {
	a.mu.Lock()
	delete(a.sessions, id)
	a.mu.Unlock()
	err := a.o.Accounts.Update(func(y *schema.YouTubeAccounts) {
		kept := y.Accounts[:0]
		for _, acc := range y.Accounts {
			if acc.ID != id {
				kept = append(kept, acc)
			}
		}
		y.Accounts = kept
		if y.Chosen == id {
			y.Chosen = ""
		}
	})
	if err != nil {
		slog.Warn("youtube accounts", "err", err)
	}
}

func (a *accounts) session(acc schema.YouTubeAccount) *youtube.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.sessions[acc.ID]; s != nil {
		return s
	}
	s := youtube.NewSession(a.o.Client, youtube.Account{
		Client:  youtube.Client{ID: acc.Client, Secret: acc.Secret},
		Refresh: acc.Refresh,
	})
	a.sessions[acc.ID] = s
	return s
}

func (a *accounts) state() (signing, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.signing == nil {
		return signing{}, false
	}
	return *a.signing, true
}

func (a *accounts) signIn() {
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if a.signing != nil && a.signing.cancel != nil {
		a.signing.cancel()
	}
	a.signing = &signing{asking: true, cancel: cancel}
	a.mu.Unlock()
	a.redraw()
	safe.Go("youtube sign-in", func() { a.run(ctx) })
}

func (a *accounts) stopSigning() {
	a.mu.Lock()
	if a.signing != nil && a.signing.cancel != nil {
		a.signing.cancel()
	}
	a.signing = nil
	a.mu.Unlock()
	a.redraw()
}

func (a *accounts) run(ctx context.Context) {
	client, err := youtube.TVClient(ctx, a.o.Client)
	if err != nil {
		a.fail(ctx, err)
		return
	}
	code, err := youtube.Ask(ctx, a.o.Client, client, a.device())
	if err != nil {
		a.fail(ctx, err)
		return
	}
	a.mu.Lock()
	if a.signing != nil && ctx.Err() == nil {
		a.signing.code, a.signing.asking = code, false
	}
	a.mu.Unlock()
	a.redraw()

	tok, err := youtube.Await(ctx, a.o.Client, client, code)
	if err != nil {
		a.fail(ctx, err)
		return
	}
	s := youtube.SignedIn(a.o.Client, client, tok)
	profile, err := s.Profile(ctx)
	if err != nil {
		slog.Warn("youtube profile", "err", err)
		profile.Name = say.T("youtube.account")
	}
	acc := schema.YouTubeAccount{
		ID:      newID(),
		Name:    profile.Name,
		Photo:   profile.Photo,
		Client:  client.ID,
		Secret:  client.Secret,
		Refresh: tok.Refresh,
	}
	err = a.o.Accounts.Update(func(y *schema.YouTubeAccounts) {
		y.Accounts = append(y.Accounts, acc)
		y.Chosen = acc.ID
	})
	if err != nil {
		a.fail(ctx, err)
		return
	}
	a.mu.Lock()
	a.sessions[acc.ID] = s
	a.signing = nil
	a.mu.Unlock()
	slog.Info("youtube signed in", "account", acc.ID)
	a.redraw()
}

func (a *accounts) fail(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	slog.Warn("youtube sign-in", "err", err)
	text := say.T("youtube.failed")
	switch {
	case errors.Is(err, youtube.ErrExpired):
		text = say.T("youtube.expired")
	case errors.Is(err, youtube.ErrDenied):
		text = say.T("youtube.refused")
	}
	a.mu.Lock()
	if a.signing != nil {
		a.signing.asking, a.signing.problem, a.signing.code = false, text, youtube.Code{}
	}
	a.mu.Unlock()
	a.redraw()
}

func (a *accounts) device() string {
	if d := a.o.Accounts.Read().Device; d != "" {
		return d
	}
	d := newID()
	if err := a.o.Accounts.Update(func(y *schema.YouTubeAccounts) {
		if y.Device == "" {
			y.Device = d
		}
		d = y.Device
	}); err != nil {
		slog.Warn("youtube accounts", "err", err)
	}
	return d
}

func (a *accounts) redraw() {
	if a.o.Redraw != nil {
		a.o.Redraw()
	}
}

func newID() string { return strings.ToLower(rand.Text()) }

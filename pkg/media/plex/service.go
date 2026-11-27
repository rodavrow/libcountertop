package plex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

const (
	stale      = 5 * time.Minute
	pageSize   = 60
	pageWindow = 3
	callTime   = 20 * time.Second
)

type Status int

const (
	SignedOut Status = iota
	Signing
	Choosing
	Connecting
	Ready
	Refused
	Unreachable
)

type Options struct {
	Settings storage.Section[schema.Plex]
	Identity Identity
	HTTP     *http.Client
	Player   PlayerOptions
	Redraw   func()
}

type Service struct {
	o      Options
	player *Player

	mu        sync.Mutex
	status    Status
	pin       PIN
	asking    bool
	problem   error
	stopSign  context.CancelFunc
	profiles  []Profile
	library   *Library
	connected bool
	home      *shelf[Hub]
	sections  *shelf[Section]
	pages     map[pageKey]*shelf[Item]
	rows      map[string]*shelf[Hub]
	seen      map[string]*shelf[Item]
	sorts     map[string]*shelf[Sort]
	letters   map[string]*shelf[Letter]
	genres    map[string]*shelf[Category]
	children  map[string]*shelf[Item]
	details   map[string]*shelf[Item]
	searches  map[string]*shelf[Hub]
}

type pageKey struct {
	list string
	n    int
}

type shelf[T any] struct {
	items  []T
	total  int
	err    error
	at     time.Time
	busy   bool
	loaded bool
}

func New(o Options) *Service {
	s := &Service{o: o}
	s.player = NewPlayer(o.Player)
	s.clear()
	return s
}

func (s *Service) clear() {
	s.home, s.sections = &shelf[Hub]{}, &shelf[Section]{}
	s.pages, s.children = map[pageKey]*shelf[Item]{}, map[string]*shelf[Item]{}
	s.rows, s.seen = map[string]*shelf[Hub]{}, map[string]*shelf[Item]{}
	s.sorts, s.letters = map[string]*shelf[Sort]{}, map[string]*shelf[Letter]{}
	s.genres = map[string]*shelf[Category]{}
	s.details, s.searches = map[string]*shelf[Item]{}, map[string]*shelf[Hub]{}
}

func (s *Service) id() Identity {
	id := s.o.Identity
	c := s.o.Settings.Read()
	if c.Client == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		c.Client = hex.EncodeToString(b)
		if err := s.o.Settings.Update(func(p *schema.Plex) { p.Client = c.Client }); err != nil {
			slog.Error("saving the plex client id failed", "err", err)
		}
	}
	id.Client = c.Client
	return id
}

func (s *Service) acct() Account {
	return Account{ID: s.id(), HTTP: s.o.HTTP}
}

func (s *Service) Status() Status {
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()
	return st
}

func (s *Service) Problem() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.problem
}

func (s *Service) Open() {
	c := s.o.Settings.Read()
	s.mu.Lock()
	switch {
	case !c.SignedIn():
		if s.status != Signing {
			s.status = SignedOut
		}
		s.mu.Unlock()
		return
	case s.connected:
		s.dropStaleLocked()
		s.mu.Unlock()
		return
	case s.status == Connecting || s.status == Choosing:
		s.mu.Unlock()
		return
	}
	s.status = Connecting
	s.mu.Unlock()
	safe.Go("plex connect", s.connect)
}

func (s *Service) dropStaleLocked() {
	old := func(at time.Time) bool { return time.Since(at) > stale }
	if old(s.home.at) && !s.home.busy {
		s.home = &shelf[Hub]{}
	}
	for k, v := range s.pages {
		if old(v.at) && !v.busy {
			delete(s.pages, k)
		}
	}
	for k, v := range s.rows {
		if old(v.at) && !v.busy {
			delete(s.rows, k)
		}
	}
	for k, v := range s.seen {
		if old(v.at) && !v.busy {
			delete(s.seen, k)
		}
	}
	for k, v := range s.details {
		if (old(v.at) || v.err != nil) && !v.busy {
			delete(s.details, k)
		}
	}
	for k, v := range s.children {
		if (old(v.at) || v.err != nil) && !v.busy {
			delete(s.children, k)
		}
	}
	s.searches = map[string]*shelf[Hub]{}
}

func (s *Service) connect() {
	ctx, cancel := context.WithTimeout(context.Background(), callTime)
	defer cancel()
	c := s.o.Settings.Read()
	a := s.acct()
	if c.Profile == "" {
		profiles, err := a.Profiles(ctx, c.Token)
		if err == nil && len(profiles) > 1 {
			s.mu.Lock()
			s.profiles, s.status = profiles, Choosing
			s.mu.Unlock()
			s.redraw()
			return
		}
	}
	s.reach(ctx, a, c)
}

func (s *Service) reach(ctx context.Context, a Account, c schema.Plex) {
	lib, err := a.reconnect(ctx, c)
	if lib == nil {
		lib, err = s.discover(ctx, a, c)
	}
	s.mu.Lock()
	switch {
	case errors.Is(err, ErrToken):
		s.status, s.problem = Refused, err
	case err != nil:
		s.status, s.problem = Unreachable, err
	default:
		s.status, s.problem, s.library, s.connected = Ready, nil, lib, true
		s.clear()
	}
	s.mu.Unlock()
	if err != nil {
		slog.Warn("plex connect", "err", err)
	}
	s.redraw()
}

func (a Account) reconnect(ctx context.Context, c schema.Plex) (*Library, error) {
	last := c.LastServer
	if c.Server == "" || last.Address == "" || last.Token == "" {
		return nil, ErrNoServer
	}
	began := time.Now()
	lib, err := a.Connect(ctx, Server{
		Machine: c.Server, Token: last.Token,
		Connections: []Connection{{URI: last.Address, Local: last.Local}},
	})
	slog.Debug("plex last server", "took", time.Since(began), "err", err)
	return lib, err
}

func (s *Service) discover(ctx context.Context, a Account, c schema.Plex) (*Library, error) {
	began := time.Now()
	servers, err := a.Servers(ctx, c.Account())
	slog.Debug("plex servers", "took", time.Since(began), "servers", len(servers), "err", err)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, ErrNoServer
	}
	srv := pick(servers, c.Server)
	lib, err := a.Connect(ctx, srv)
	if err != nil {
		return nil, err
	}
	if e := s.o.Settings.Update(func(p *schema.Plex) {
		p.Server = srv.Machine
		p.LastServer = schema.PlexServer{Address: lib.Base, Token: lib.Token, Local: lib.Local}
	}); e != nil {
		slog.Error("saving the plex server failed", "err", e)
	}
	return lib, nil
}

func pick(servers []Server, want string) Server {
	for _, sv := range servers {
		if sv.Machine == want {
			return sv
		}
	}
	for _, sv := range servers {
		if sv.Owned {
			return sv
		}
	}
	return servers[0]
}

func (s *Service) SignIn() {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.stopSign != nil {
		s.stopSign()
	}
	s.status, s.asking, s.problem, s.pin, s.stopSign = Signing, true, nil, PIN{}, cancel
	s.mu.Unlock()
	s.redraw()
	safe.Go("plex sign-in", func() { s.signIn(ctx) })
}

func (s *Service) signIn(ctx context.Context) {
	a := s.acct()
	pin, err := a.Ask(ctx)
	if err != nil {
		s.signFailed(ctx, err)
		return
	}
	s.mu.Lock()
	s.pin, s.asking = pin, false
	s.mu.Unlock()
	s.redraw()
	token, err := a.Await(ctx, pin)
	if err != nil {
		s.signFailed(ctx, err)
		return
	}
	if err := s.o.Settings.Update(func(p *schema.Plex) {
		p.Token, p.Profile, p.ProfileToken, p.Server = token, "", "", ""
		p.LastServer = schema.PlexServer{}
	}); err != nil {
		s.signFailed(ctx, err)
		return
	}
	s.mu.Lock()
	s.stopSign, s.status, s.connected = nil, Connecting, false
	s.mu.Unlock()
	s.redraw()
	s.connect()
}

func (s *Service) signFailed(ctx context.Context, err error) {
	if ctx.Err() != nil && !errors.Is(err, ErrExpired) {
		return
	}
	slog.Warn("plex sign-in", "err", err)
	s.mu.Lock()
	s.asking, s.problem, s.pin = false, err, PIN{}
	s.mu.Unlock()
	s.redraw()
}

func (s *Service) Code() (PIN, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pin, s.asking, s.problem
}

func (s *Service) CancelSignIn() {
	s.mu.Lock()
	if s.stopSign != nil {
		s.stopSign()
		s.stopSign = nil
	}
	if s.status == Signing {
		s.status = SignedOut
	}
	s.mu.Unlock()
	s.redraw()
}

func (s *Service) SignOut() {
	s.player.Stop()
	if err := s.o.Settings.Update(func(p *schema.Plex) {
		p.Token, p.Profile, p.ProfileToken, p.Server = "", "", "", ""
		p.LastServer = schema.PlexServer{}
	}); err != nil {
		slog.Error("clearing the plex sign-in failed", "err", err)
	}
	s.mu.Lock()
	s.status, s.library, s.connected, s.profiles, s.problem = SignedOut, nil, false, nil, nil
	s.clear()
	s.mu.Unlock()
	s.redraw()
}

func (s *Service) Profiles() []Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.profiles
}

func (s *Service) Current() string { return s.o.Settings.Read().Profile }

func (s *Service) SwitchProfile() {
	s.player.Stop()
	s.mu.Lock()
	s.connected, s.library = false, nil
	s.mu.Unlock()
	if err := s.o.Settings.Update(func(p *schema.Plex) {
		p.Profile, p.ProfileToken, p.LastServer = "", "", schema.PlexServer{}
	}); err != nil {
		slog.Error("clearing the plex profile failed", "err", err)
	}
	s.mu.Lock()
	s.status = Connecting
	s.mu.Unlock()
	s.redraw()
	safe.Go("plex profiles", s.connect)
}

func (s *Service) Choose(p Profile, pin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTime)
	defer cancel()
	c := s.o.Settings.Read()
	a := s.acct()
	token, err := a.Switch(ctx, c.Token, p.UUID, pin)
	if err != nil {
		return err
	}
	if err := s.o.Settings.Update(func(pl *schema.Plex) {
		pl.Profile, pl.ProfileToken, pl.LastServer = p.UUID, token, schema.PlexServer{}
	}); err != nil {
		return err
	}
	s.mu.Lock()
	s.status = Connecting
	s.mu.Unlock()
	s.redraw()
	safe.Go("plex connect", func() {
		ctx, cancel := context.WithTimeout(context.Background(), callTime)
		defer cancel()
		s.reach(ctx, a, s.o.Settings.Read())
	})
	return nil
}

func (s *Service) Library() *Library {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.library
}

func (s *Service) Player() *Player { return s.player }

func (s *Service) Play(it Item, resume bool) error {
	lib := s.Library()
	if lib == nil {
		return ErrNoServer
	}
	from := time.Duration(0)
	if resume {
		from = it.Offset
	}
	return s.player.Play(lib, it, from)
}

func (s *Service) Home() ([]Hub, bool, error) {
	return load(
		s,
		func() *shelf[Hub] { return s.home },
		func(ctx context.Context, l *Library) ([]Hub, int, error) {
			began := time.Now()
			rows, err := l.Promoted(ctx)
			slog.Debug("plex home rows", "took", time.Since(began), "rows", rowCounts(rows),
				"err", err)
			return rows, 0, err
		},
	)
}

func rowCounts(hs []Hub) string {
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		parts = append(parts, h.Title+"="+strconv.Itoa(len(h.Items)))
	}
	return strings.Join(parts, ", ")
}

func (s *Service) Sections() ([]Section, bool, error) {
	return load(
		s,
		func() *shelf[Section] { return s.sections },
		func(ctx context.Context, l *Library) ([]Section, int, error) {
			all, err := l.Sections(ctx)
			var keep []Section
			for _, sc := range all {
				if sc.Type == "movie" || sc.Type == "show" || sc.Type == "artist" {
					keep = append(keep, sc)
				}
			}
			return keep, 0, err
		},
	)
}

func PageSize() int { return pageSize }

func (s *Service) Page(section string, q Query, n int) ([]Item, int, bool, error) {
	k := pageKey{
		list: section + "|" + q.Sort + "|" + strconv.FormatBool(q.Unwatched) + "|" + q.Genre, n: n,
	}
	s.mu.Lock()
	for o, sh := range s.pages {
		far := n > 0 && o.n > 0 && (o.n < n-pageWindow || o.n > n+pageWindow)
		if !sh.busy && (o.list != k.list || far) {
			delete(s.pages, o)
		}
	}
	sh := s.pages[k]
	if sh == nil {
		sh = &shelf[Item]{}
		s.pages[k] = sh
	}
	s.mu.Unlock()
	items, loaded, err := load(
		s,
		func() *shelf[Item] { return sh },
		func(ctx context.Context, l *Library) ([]Item, int, error) {
			return l.Items(ctx, section, q, n*pageSize, pageSize)
		},
	)
	s.mu.Lock()
	total := sh.total
	s.mu.Unlock()
	return items, total, loaded, err
}

func (s *Service) Random(section string, q Query, total int) (Item, error) {
	lib := s.Library()
	if lib == nil {
		return Item{}, ErrNoServer
	}
	if total <= 0 {
		return Item{}, ErrEmpty
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTime)
	defer cancel()
	items, _, err := lib.Items(ctx, section, q, mrand.IntN(total), 1)
	if err != nil {
		return Item{}, err
	}
	if len(items) == 0 {
		return Item{}, ErrEmpty
	}
	return items[0], nil
}

func (s *Service) Rows(section string) ([]Hub, bool, error) {
	return load(
		s,
		func() *shelf[Hub] { return keyed(s.rows, section) },
		func(ctx context.Context, l *Library) ([]Hub, int, error) {
			began := time.Now()
			rows, err := l.SectionHubs(ctx, section)
			slog.Debug("plex library rows", "section", section, "took", time.Since(began),
				"rows", rowCounts(rows), "err", err)
			return rows, 0, err
		},
	)
}

func (s *Service) SeeAll(identifier string) ([]Item, bool, error) {
	return load(
		s,
		func() *shelf[Item] { return keyed(s.seen, identifier) },
		func(ctx context.Context, l *Library) ([]Item, int, error) {
			items, err := l.HubItems(ctx, identifier)
			return items, len(items), err
		},
	)
}

func (s *Service) Sorts(section string) ([]Sort, bool, error) {
	return load(
		s,
		func() *shelf[Sort] { return keyed(s.sorts, section) },
		func(ctx context.Context, l *Library) ([]Sort, int, error) {
			sorts, err := l.Sorts(ctx, section)
			return sorts, len(sorts), err
		},
	)
}

func (s *Service) Categories(section string) ([]Category, bool, error) {
	return load(
		s,
		func() *shelf[Category] { return keyed(s.genres, section) },
		func(ctx context.Context, l *Library) ([]Category, int, error) {
			cats, err := l.Categories(ctx, section)
			return cats, len(cats), err
		},
	)
}

func (s *Service) Tinted(path, blend string, w, h int) string {
	lib := s.Library()
	if lib == nil {
		return ""
	}
	return lib.Tinted(path, blend, w, h)
}

func (s *Service) Letters(section string) ([]Letter, bool, error) {
	return load(
		s,
		func() *shelf[Letter] { return keyed(s.letters, section) },
		func(ctx context.Context, l *Library) ([]Letter, int, error) {
			letters, err := l.Letters(ctx, section)
			return letters, len(letters), err
		},
	)
}

func (s *Service) Children(rk string) ([]Item, bool, error) {
	return load(
		s,
		func() *shelf[Item] { return keyed(s.children, rk) },
		func(ctx context.Context, l *Library) ([]Item, int, error) {
			items, err := l.Children(ctx, rk)
			return items, len(items), err
		},
	)
}

func (s *Service) Item(rk string) (Item, bool, error) {
	if rk == "" {
		return Item{}, false, nil
	}
	items, loaded, err := load(
		s,
		func() *shelf[Item] { return keyed(s.details, rk) },
		func(ctx context.Context, l *Library) ([]Item, int, error) {
			it, err := l.Item(ctx, rk)
			return []Item{it}, 1, err
		},
	)
	if len(items) == 0 {
		return Item{}, loaded, err
	}
	return items[0], loaded, err
}

func (s *Service) Search(q string) ([]Hub, bool, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, true, nil
	}
	return load(
		s,
		func() *shelf[Hub] { return keyed(s.searches, strings.ToLower(q)) },
		func(ctx context.Context, l *Library) ([]Hub, int, error) {
			hubs, err := l.Search(ctx, q)
			return hubs, 0, err
		},
	)
}

func (s *Service) Picture(path string, w, h int) string {
	lib := s.Library()
	if lib == nil {
		return ""
	}
	return lib.Picture(path, w, h)
}

func (s *Service) Fetch(ctx context.Context, picture string) ([]byte, error) {
	lib := s.Library()
	if lib == nil {
		return nil, ErrNoServer
	}
	return lib.fetch(ctx, picture)
}

func keyed[T any](m map[string]*shelf[T], k string) *shelf[T] {
	sh := m[k]
	if sh == nil {
		sh = &shelf[T]{}
		m[k] = sh
	}
	return sh
}

func load[T any](
	s *Service,
	which func() *shelf[T],
	get func(context.Context, *Library) ([]T, int, error),
) ([]T, bool, error) {
	s.mu.Lock()
	sh := which()
	lib := s.library
	due := lib != nil && !sh.busy && (!sh.loaded || sh.at.IsZero())
	if due {
		sh.busy = true
	}
	items, loaded, err := sh.items, sh.loaded, sh.err
	s.mu.Unlock()
	if due {
		safe.Go("plex load", func() {
			ctx, cancel := context.WithTimeout(context.Background(), callTime)
			defer cancel()
			got, total, err := get(ctx, lib)
			s.mu.Lock()
			if err == nil || !sh.loaded {
				sh.items, sh.total = got, total
			}
			sh.err, sh.loaded, sh.busy, sh.at = err, true, false, time.Now()
			if errors.Is(err, ErrToken) {
				s.status, s.problem, s.connected = Refused, err, false
			}
			s.mu.Unlock()
			if err != nil {
				slog.Warn("plex load", "err", err)
			}
			s.redraw()
		})
	}
	return items, loaded, err
}

func (s *Service) SetWatched(it Item, watched bool) error {
	lib := s.Library()
	if lib == nil {
		return ErrNoServer
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTime)
	defer cancel()
	if err := lib.SetWatched(ctx, it, watched); err != nil {
		return err
	}
	s.mu.Lock()
	if !s.home.busy {
		s.home.at = time.Time{}
	}
	for _, sh := range s.rows {
		if !sh.busy {
			sh.at = time.Time{}
		}
	}
	for _, sh := range s.pages {
		if !sh.busy {
			sh.at = time.Time{}
		}
	}
	for _, sh := range s.children {
		if !sh.busy {
			sh.at = time.Time{}
		}
	}
	if sh := s.details[it.RatingKey]; sh != nil && !sh.busy {
		sh.at = time.Time{}
	}
	s.mu.Unlock()
	s.redraw()
	return nil
}

func (s *Service) SetSubtitle(it Item, sub Subtitle) error {
	lib := s.Library()
	if lib == nil {
		return ErrNoServer
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTime)
	defer cancel()
	if err := lib.SetSubtitle(ctx, sub.Part, sub.ID); err != nil {
		return err
	}
	s.mu.Lock()
	if sh := s.details[it.RatingKey]; sh != nil && !sh.busy {
		sh.at = time.Time{}
	}
	s.mu.Unlock()
	s.redraw()
	return nil
}

func (s *Service) RefreshHome() {
	s.mu.Lock()
	if !s.home.busy {
		s.home.at = time.Time{}
	}
	s.mu.Unlock()
	s.redraw()
}

func (s *Service) HomeRefreshing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.home.busy && s.home.loaded
}

func (s *Service) RefreshLibrary(section string) {
	s.mu.Lock()
	if sh := s.rows[section]; sh != nil && !sh.busy {
		sh.at = time.Time{}
	}
	if sh := s.letters[section]; sh != nil && !sh.busy {
		sh.at = time.Time{}
	}
	for k, sh := range s.pages {
		if strings.HasPrefix(k.list, section+"|") && !sh.busy {
			sh.at = time.Time{}
		}
	}
	s.mu.Unlock()
	s.redraw()
}

func (s *Service) LibraryRefreshing(section string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sh := s.rows[section]; sh != nil && sh.busy && sh.loaded {
		return true
	}
	for k, sh := range s.pages {
		if strings.HasPrefix(k.list, section+"|") && sh.busy && sh.loaded {
			return true
		}
	}
	return false
}

func (s *Service) redraw() {
	if s.o.Redraw != nil {
		s.o.Redraw()
	}
}

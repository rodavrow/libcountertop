package mealie

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/hook"
	"github.com/ygelfand/libcountertop/pkg/match"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

const (
	perPage   = 36
	findPage  = 12
	stale     = 5 * time.Minute
	recipeIDs = 400
	TimerID   = "recipe-step"
)

type Timers interface {
	Count(id, name string, d time.Duration)
}

type Options struct {
	Settings storage.Section[schema.Mealie]
	Device   uint32
	Timers   Timers
	Redraw   func()
}

type Status int

const (
	Unset Status = iota
	Connecting
	Ready
	Refused
	Unreachable
)

func (s Status) Says() string {
	return say.T("recipes.status." + [...]string{
		"unset", "connecting", "ready", "token", "unreachable",
	}[s])
}

type ShowKind int

const (
	ShowBrowse ShowKind = iota
	ShowRecipe
	ShowCook
)

type Show struct {
	Kind  ShowKind
	Query string
	Slug  string
}

type Shelf struct {
	Items  []Summary
	Err    error
	Loaded bool
	More   bool
}

type shelf struct {
	Shelf
	page int
	busy bool
	at   time.Time
}

type dish struct {
	recipe Recipe
	err    error
	done   bool
	busy   bool
	at     time.Time
}

type Session struct {
	Recipe   Recipe
	Step     int
	Servings float64
}

func (s Session) Scale() float64 {
	if s.Recipe.Servings <= 0 || s.Servings <= 0 {
		return 1
	}
	return s.Servings / s.Recipe.Servings
}

func (s Session) Uses(step int) []Ingredient {
	if step < 0 || step >= len(s.Recipe.Steps) {
		return nil
	}
	var out []Ingredient
	for _, ref := range s.Recipe.Steps[step].Ingredients {
		for _, in := range s.Recipe.Ingredients {
			if ref.ID != "" && in.Reference == ref.ID {
				out = append(out, in)
			}
		}
	}
	return out
}

type Kitchen struct {
	Asked hook.Hook[Show]

	o     Options
	knobs *setting.Controls[schema.Mealie]

	mu        sync.Mutex
	client    *Client
	built     schema.Mealie
	status    Status
	user      string
	favorites map[string]bool
	shelves   map[string]*shelf
	dishes    map[string]*dish
	session   *Session
	checking  bool
}

func New(o Options) *Kitchen {
	k := &Kitchen{o: o, shelves: map[string]*shelf{}, dishes: map[string]*dish{}}
	k.knobs = &setting.Controls[schema.Mealie]{
		Table:  NewTable(),
		Device: o.Device,
		Read:   o.Settings.Read,
		Save:   k.save,
	}
	return k
}

func (k *Kitchen) Name() string { return "mealie" }

func (k *Kitchen) Settings() *setting.Controls[schema.Mealie] { return k.knobs }

func (k *Kitchen) Entities() []esphome.Entity { return k.knobs.Entities() }

func (k *Kitchen) Restore(c schema.Mealie) { k.knobs.PublishFrom(c) }

func (k *Kitchen) save(s Setting, v string) error {
	if err := setting.Store(k.o.Settings, s, v); err != nil {
		return err
	}
	k.mu.Lock()
	k.client, k.status, k.user, k.favorites = nil, Unset, "", nil
	k.shelves, k.dishes = map[string]*shelf{}, map[string]*dish{}
	k.mu.Unlock()
	k.Check()
	return nil
}

func (k *Kitchen) Configured() bool {
	c := k.o.Settings.Read()
	return strings.TrimSpace(c.Server) != "" && strings.TrimSpace(c.Token) != ""
}

func (k *Kitchen) current() *Client {
	k.mu.Lock()
	defer k.mu.Unlock()
	want := k.o.Settings.Read()
	if k.client == nil || want != k.built {
		k.client, k.built = NewClient(want.Server, want.Token), want
	}
	return k.client
}

func (k *Kitchen) Status() Status {
	if !k.Configured() {
		return Unset
	}
	k.mu.Lock()
	st, checking := k.status, k.checking
	k.mu.Unlock()
	if st == Unset && !checking {
		k.Check()
		return Connecting
	}
	return st
}

func (k *Kitchen) Check() {
	if !k.Configured() {
		return
	}
	k.mu.Lock()
	if k.checking {
		k.mu.Unlock()
		return
	}
	k.checking = true
	if k.status == Unset {
		k.status = Connecting
	}
	k.mu.Unlock()
	safe.Go("mealie check", func() {
		ctx, cancel := context.WithTimeout(context.Background(), askTime)
		defer cancel()
		c := k.current()
		me, err := c.Self(ctx)
		var favs []string
		if err == nil {
			favs, err = c.Favorites(ctx)
		}
		k.mu.Lock()
		k.checking = false
		switch {
		case err == nil:
			k.status, k.user = Ready, me.ID
			k.favorites = map[string]bool{}
			for _, id := range favs {
				k.favorites[id] = true
			}
		case errors.Is(err, ErrToken):
			k.status = Refused
		default:
			k.status = Unreachable
		}
		k.mu.Unlock()
		if err != nil {
			slog.Warn("mealie unreachable", "server", c.Base, "err", err)
		}
		k.redraw()
	})
}

func (k *Kitchen) Refresh() {
	k.mu.Lock()
	for key, s := range k.shelves {
		if !s.busy && time.Since(s.at) > stale {
			delete(k.shelves, key)
		}
	}
	for slug, d := range k.dishes {
		if !d.busy && (d.err != nil || time.Since(d.at) > stale) {
			delete(k.dishes, slug)
		}
	}
	k.mu.Unlock()
	k.Check()
}

func shelfKey(search string, favorites bool) string {
	key := strings.ToLower(strings.TrimSpace(search))
	if favorites {
		return "\x00fav\x00" + key
	}
	return key
}

func (k *Kitchen) List(search string, favorites bool) Shelf {
	key := shelfKey(search, favorites)
	k.mu.Lock()
	s := k.shelves[key]
	if s == nil {
		s = &shelf{}
		k.shelves[key] = s
	}
	due := !s.busy && !s.Loaded
	if due {
		s.busy = true
	}
	out := s.Shelf
	out.Items = slices.Clone(s.Items)
	k.mu.Unlock()
	if due {
		k.load(s, search, favorites, 1)
	}
	return out
}

func (k *Kitchen) More(search string, favorites bool) {
	key := shelfKey(search, favorites)
	k.mu.Lock()
	s := k.shelves[key]
	if s == nil || s.busy || !s.More {
		k.mu.Unlock()
		return
	}
	s.busy = true
	next := s.page + 1
	k.mu.Unlock()
	k.load(s, search, favorites, next)
}

func (k *Kitchen) load(s *shelf, search string, favorites bool, page int) {
	safe.Go("mealie recipes", func() {
		ctx, cancel := context.WithTimeout(context.Background(), askTime)
		defer cancel()
		q := Query{Search: search, Page: page, Per: perPage}
		var got Page
		var err error
		if favorites {
			q.IDs = k.favoriteIDs()
		}
		if favorites && len(q.IDs) == 0 {
			err = k.waitFavorites(ctx)
			q.IDs = k.favoriteIDs()
		}
		if err == nil && (!favorites || len(q.IDs) > 0) {
			got, err = k.current().Recipes(ctx, q)
		}
		k.mu.Lock()
		s.busy, s.Loaded, s.Err, s.at = false, true, err, time.Now()
		if err == nil {
			if page == 1 {
				s.Items = nil
			}
			s.Items = append(s.Items, got.Items...)
			s.page = page
			s.More = got.Pages > page
		}
		k.mu.Unlock()
		if err != nil {
			slog.Warn("mealie recipes", "search", search, "favorites", favorites, "err", err)
		}
		k.redraw()
	})
}

func (k *Kitchen) waitFavorites(ctx context.Context) error {
	k.mu.Lock()
	known := k.favorites != nil
	k.mu.Unlock()
	if known {
		return nil
	}
	favs, err := k.current().Favorites(ctx)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.favorites = map[string]bool{}
	for _, id := range favs {
		k.favorites[id] = true
	}
	k.mu.Unlock()
	return nil
}

func (k *Kitchen) favoriteIDs() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	ids := make([]string, 0, len(k.favorites))
	for id, on := range k.favorites {
		if on {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids[:min(len(ids), recipeIDs)]
}

func (k *Kitchen) Favorite(id string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.favorites[id]
}

func (k *Kitchen) SetFavorite(r Summary, on bool) {
	k.mu.Lock()
	if k.favorites == nil {
		k.favorites = map[string]bool{}
	}
	was := k.favorites[r.ID]
	k.favorites[r.ID] = on
	user := k.user
	for key := range k.shelves {
		if strings.HasPrefix(key, "\x00fav\x00") {
			delete(k.shelves, key)
		}
	}
	k.mu.Unlock()
	k.redraw()
	safe.Go("mealie favorite", func() {
		ctx, cancel := context.WithTimeout(context.Background(), askTime)
		defer cancel()
		c := k.current()
		var err error
		if user == "" {
			var me User
			if me, err = c.Self(ctx); err == nil {
				user = me.ID
			}
		}
		if err == nil {
			err = c.SetFavorite(ctx, user, r.Slug, on)
		}
		if err != nil {
			slog.Warn("mealie favorite", "recipe", r.Slug, "on", on, "err", err)
			k.mu.Lock()
			k.favorites[r.ID] = was
			k.mu.Unlock()
			k.redraw()
		}
	})
}

func (k *Kitchen) Recipe(slug string) (Recipe, error, bool) {
	k.mu.Lock()
	d := k.dishes[slug]
	if d == nil {
		d = &dish{}
		k.dishes[slug] = d
	}
	due := !d.busy && !d.done
	if due {
		d.busy = true
	}
	r, err, done := d.recipe, d.err, d.done
	k.mu.Unlock()
	if due {
		safe.Go("mealie recipe", func() {
			ctx, cancel := context.WithTimeout(context.Background(), askTime)
			defer cancel()
			got, err := k.current().Recipe(ctx, slug)
			k.mu.Lock()
			d.recipe, d.err, d.done, d.busy, d.at = got, err, true, false, time.Now()
			k.mu.Unlock()
			if err != nil {
				slog.Warn("mealie recipe", "slug", slug, "err", err)
			}
			k.redraw()
		})
	}
	return r, err, done
}

func (k *Kitchen) fetch(ctx context.Context, slug string) (Recipe, error) {
	k.mu.Lock()
	if d := k.dishes[slug]; d != nil && d.done && d.err == nil && time.Since(d.at) <= stale {
		r := d.recipe
		k.mu.Unlock()
		return r, nil
	}
	k.mu.Unlock()
	r, err := k.current().Recipe(ctx, slug)
	if err == nil {
		k.mu.Lock()
		k.dishes[slug] = &dish{recipe: r, done: true, at: time.Now()}
		k.mu.Unlock()
	}
	return r, err
}

func (k *Kitchen) Find(ctx context.Context, said string) (Summary, []Summary, error) {
	page, err := k.current().Recipes(ctx, Query{Search: said, Page: 1, Per: findPage})
	if err != nil || len(page.Items) == 0 {
		return Summary{}, page.Items, err
	}
	candidates := make([]match.Candidate, 0, len(page.Items))
	for _, s := range page.Items {
		candidates = append(candidates, match.Candidate{Key: s.Slug, Name: s.Name})
	}
	if slug, ok := match.Closest(said, candidates); ok {
		i := slices.IndexFunc(page.Items, func(s Summary) bool { return s.Slug == slug })
		return page.Items[i], page.Items, nil
	}
	return page.Items[0], page.Items, nil
}

func (k *Kitchen) PictureURL(s Summary, size string) string {
	if !s.HasImage() {
		return ""
	}
	return k.current().PictureURL(s.ID, size)
}

func (k *Kitchen) Picture(ctx context.Context, address string) ([]byte, error) {
	return k.current().Picture(ctx, address)
}

func (k *Kitchen) Cook(r Recipe) {
	k.mu.Lock()
	k.session = &Session{Recipe: r, Servings: r.Servings}
	k.mu.Unlock()
	k.redraw()
}

func (k *Kitchen) Session() (Session, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.session == nil {
		return Session{}, false
	}
	return *k.session, true
}

func (k *Kitchen) Cooking(slug string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.session != nil && k.session.Recipe.Slug == slug
}

func (k *Kitchen) Move(by int) (Session, bool) {
	k.mu.Lock()
	if k.session == nil {
		k.mu.Unlock()
		return Session{}, false
	}
	last := len(k.session.Recipe.Steps) - 1
	to := min(max(k.session.Step+by, 0), max(last, 0))
	moved := to != k.session.Step
	k.session.Step = to
	s := *k.session
	k.mu.Unlock()
	if moved {
		k.redraw()
	}
	return s, moved
}

func (k *Kitchen) Goto(step int) {
	k.mu.Lock()
	if k.session != nil && step >= 0 && step < len(k.session.Recipe.Steps) {
		k.session.Step = step
	}
	k.mu.Unlock()
	k.redraw()
}

func (k *Kitchen) Serve(servings float64) {
	k.mu.Lock()
	if k.session != nil && servings > 0 {
		k.session.Servings = servings
	}
	k.mu.Unlock()
	k.redraw()
}

func (k *Kitchen) Done() {
	k.mu.Lock()
	k.session = nil
	k.mu.Unlock()
	k.redraw()
}

func (k *Kitchen) StepTimer() (time.Duration, bool) {
	s, ok := k.Session()
	if !ok || k.o.Timers == nil || s.Step >= len(s.Recipe.Steps) {
		return 0, false
	}
	d := s.Recipe.Steps[s.Step].Duration()
	if d <= 0 {
		return 0, false
	}
	name := s.Recipe.Name
	if title := strings.TrimSpace(s.Recipe.Steps[s.Step].Title); title != "" {
		name = title
	}
	k.o.Timers.Count(TimerID, name, d)
	return d, true
}

func (k *Kitchen) redraw() {
	if k.o.Redraw != nil {
		k.o.Redraw()
	}
}

package plexapp

import (
	"log/slog"
	"sync"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"
	_ "golang.org/x/image/webp"

	"github.com/ygelfand/libcountertop/pkg/display/apps"
	"github.com/ygelfand/libcountertop/pkg/display/covers"
	"github.com/ygelfand/libcountertop/pkg/display/navigation"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/plex"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const (
	typePause   = 600 * time.Millisecond
	spinnerPart = 7
	pictures    = 200
	homeTab     = "home"
	searchTab   = "search"
)

type Options struct {
	UI     *widgets.UI
	Shell  *shell.Shell
	Plex   *plex.Service
	Redraw func()
}

type Browse struct{}

func (*Browse) Covers() bool { return true }

func (*Browse) Timeout() time.Duration { return shell.AppTimeout }

type Title struct{}

func (*Title) Covers() bool { return true }

func (*Title) Timeout() time.Duration { return shell.AppTimeout }

type App struct {
	o        Options
	browse   *Browse
	title    *Title
	pictures *covers.Covers

	mu       sync.Mutex
	tab      string
	typed    string
	query    string
	pause    *time.Timer
	trail    []string
	pin      string
	picking  *plex.Profile
	wrong    bool
	failed   string
	starting string
	view     string
	genre    string
	row      *plex.Hub
	listing  plex.Query
}

func New(o Options) *App {
	a := &App{o: o, browse: &Browse{}, title: &Title{}, tab: homeTab}
	a.pictures = covers.New(covers.Options{
		Prefix: "plex",
		Shape:  covers.Wide,
		Get:    o.Plex.Fetch,
		Redraw: a.redraw,
		Keep:   pictures,
	})
	o.Shell.Changed.Listen(func(c shell.Change) {
		if c.From == a.title && c.Back {
			a.mu.Lock()
			if n := len(a.trail); n > 0 {
				a.trail = a.trail[:n-1]
			}
			a.mu.Unlock()
		}
	})
	return a
}

func (a *App) App() apps.App {
	return apps.App{
		ID:   schema.AppPlex,
		Name: func() string { return say.T("plex.app") },
		Icon: "mdi:plex",
		Open: a.open,
	}
}

func (a *App) open() {
	a.o.Plex.Open()
	a.o.Shell.Push(a.browse)
}

func (a *App) Screen(v shell.View) *navigation.Screen {
	switch v {
	case a.browse:
		return &navigation.Screen{View: v, Build: a.build}
	case a.title:
		return &navigation.Screen{
			Title: say.T("plex.app"), Head: a.titleHead, View: v, Build: a.titled, Fixed: true,
		}
	}
	return nil
}

func (a *App) browsing(w *gogui.Window) gogui.View {
	s := a.o.Plex
	switch s.Status() {
	case plex.SignedOut:
		return a.signedOut(w)
	case plex.Signing:
		return a.signing(w)
	case plex.Choosing:
		return a.choosing(w)
	case plex.Connecting:
		return note(say.T("plex.connecting"))
	case plex.Refused:
		return a.trouble(say.T("plex.refused"))
	case plex.Unreachable:
		return a.trouble(say.T("plex.unreachable"))
	}
	a.mu.Lock()
	tab, row := a.tab, a.row
	a.mu.Unlock()
	if row != nil {
		return a.seeing(w, *row)
	}
	switch tab {
	case homeTab:
		return a.home(w)
	case searchTab:
		return a.searching(w)
	}
	return a.library(w, tab)
}

func (a *App) choose(tab string) {
	a.mu.Lock()
	if a.tab != tab {
		a.view, a.listing, a.genre = rowsView, plex.Query{}, ""
	}
	a.tab, a.row = tab, nil
	a.mu.Unlock()
	a.redraw()
}

func (a *App) home(w *gogui.Window) gogui.View {
	hubs, loaded, err := a.o.Plex.Home()
	switch {
	case !loaded:
		return note(say.T("home.loading"))
	case err != nil && len(hubs) == 0:
		return note(say.T("plex.unreachable"))
	case len(hubs) == 0:
		return note(say.T("plex.empty"))
	}
	rows := make([]gogui.View, 0, len(hubs))
	for i, h := range hubs {
		rows = append(rows, a.hubRow(w, "plex-hub-"+itoa(i), h))
	}
	return a.scroller("plex-home", rows)
}

func (a *App) searching(w *gogui.Window) gogui.View {
	a.mu.Lock()
	typed, query := a.typed, a.query
	a.mu.Unlock()
	field := a.o.UI.Search(gogui.InputCfg{
		ID:          "plex-search",
		Text:        typed,
		Placeholder: say.T("plex.search.hint"),
		Sizing:      gogui.FillFit,
		OnTextChanged: func(text string, e gogui.EventCtx) {
			a.typing(text)
			e.Window.InvalidateLayout()
		},
	}, a.commit)
	views := []gogui.View{field}
	if query != "" {
		hubs, loaded, _ := a.o.Plex.Search(query)
		switch {
		case !loaded:
			views = append(views, note(say.T("home.loading")))
		case len(hubs) == 0:
			views = append(views, note(say.T("plex.nothing")))
		default:
			rows := make([]gogui.View, 0, len(hubs))
			for i, h := range hubs {
				rows = append(rows, a.hubRow(w, "plex-found-"+itoa(i), h))
			}
			views = append(views, a.scroller("plex-search-results", rows))
		}
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: views,
	})
}

func (a *App) typing(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.typed = text
	if a.pause != nil {
		a.pause.Stop()
	}
	a.pause = time.AfterFunc(typePause, a.commit)
}

func (a *App) commit() {
	a.mu.Lock()
	if a.pause != nil {
		a.pause.Stop()
	}
	changed := a.query != a.typed
	a.query = a.typed
	a.mu.Unlock()
	if changed {
		a.redraw()
	}
}

func (a *App) show(it plex.Item) {
	if it.Playable() && it.Type == "track" {
		a.play(it, false)
		return
	}
	a.mu.Lock()
	on := a.o.Shell.Visible(a.title)
	if on && len(a.trail) > 0 {
		a.trail = append(a.trail, it.RatingKey)
	} else {
		a.trail = []string{it.RatingKey}
	}
	a.mu.Unlock()
	if on {
		a.redraw()
		return
	}
	a.o.Shell.Push(a.title)
}

func (a *App) play(it plex.Item, resume bool) {
	a.mu.Lock()
	a.starting, a.failed = it.RatingKey, ""
	a.mu.Unlock()
	a.redraw()
	safe.Go("plex play", func() {
		err := a.o.Plex.Play(it, resume)
		if err != nil {
			slog.Warn("plex play", "item", it.RatingKey, "err", err)
		}
		a.mu.Lock()
		a.starting = ""
		if err != nil {
			a.failed = it.RatingKey
		}
		a.mu.Unlock()
		a.redraw()
	})
}

func (a *App) redraw() {
	if a.o.Redraw != nil {
		a.o.Redraw()
	}
}

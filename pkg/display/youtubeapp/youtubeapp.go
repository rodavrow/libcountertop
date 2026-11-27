package youtubeapp

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/apps"
	"github.com/ygelfand/libcountertop/pkg/display/covers"
	"github.com/ygelfand/libcountertop/pkg/display/icons"
	"github.com/ygelfand/libcountertop/pkg/display/navigation"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/media/sponsorblock"
	"github.com/ygelfand/libcountertop/pkg/media/youtube"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

const (
	activateSite = "youtube.com/activate"
	activateURL  = "https://www.youtube.com/activate?user_code="
)

const (
	artLimit       = 4 << 20
	railUnits      = 3.0
	pageSide       = 0.5
	listSide       = 0.4
	cardUnits      = 4.2
	shortUnits     = 2.8
	shortGridUnits = 4.0
	shortBare      = 1.4
	shortRows      = 2
	peek           = 0.5
	faceKeep       = 32
	refreshZ       = 1000
)

type tab int

const (
	homeTab tab = iota
	shortsTab
	subscriptionsTab
	historyTab
	laterTab
	searchTab
	accountsTab
)

type Options struct {
	UI       *widgets.UI
	Shell    *shell.Shell
	Accounts storage.Section[schema.YouTubeAccounts]
	Resolver *youtube.Resolver
	Output   playback.Output
	Skip     func() []string
	Autoplay func() bool
	Client   *http.Client
	Redraw   func()
}

type View struct{}

func (*View) Covers() bool { return true }

func (*View) Timeout() time.Duration { return shell.AppTimeout }

func (*View) Presents() bool { return true }

type App struct {
	o        Options
	view     *View
	accounts *accounts
	feeds    *feeds
	player   *youtube.Player
	thumbs   *covers.Covers
	faces    *covers.Covers
	titles   *titles

	mu     sync.Mutex
	tab    tab
	draft  string
	query  string
	topped map[string]bool
	began  time.Time

	spinning atomic.Bool
}

func New(o Options) *App {
	a := &App{o: o, view: &View{}, topped: map[string]bool{}}
	a.accounts = newAccounts(o)
	if o.Resolver != nil {
		o.Resolver.Profile = a.accounts.profile
		o.Resolver.Account = a.accounts.chosen
	}
	a.feeds = newFeeds(a.redraw)
	a.titles = newTitles(o.Client, a.redraw)
	a.thumbs = covers.New(covers.Options{
		Prefix: "youtubeapp", Shape: covers.Wide, Get: a.download, Redraw: a.redraw,
	})
	a.faces = covers.New(covers.Options{
		Prefix: "youtubeapp-face", Get: a.download, Redraw: a.redraw, Keep: faceKeep,
	})
	a.player = youtube.NewPlayer(context.Background(), youtube.PlayerOptions{
		Resolver: o.Resolver,
		Output:   o.Output,
		Skips:    a.skips,
		Related:  a.related,
		Autoplay: o.Autoplay,
		Label:    say.T("youtube.app"),
		Changed:  a.redraw,
	})
	o.Shell.Changed.Listen(a.shown)
	return a
}

func (a *App) shown(c shell.Change) {
	if c.To != shell.View(a.view) || c.From == c.To {
		return
	}
	if key, _, ok := a.viewer(); ok {
		a.feeds.refresh(key)
	}
}

func (a *App) App() apps.App {
	return apps.App{
		ID:   schema.AppYouTube,
		Name: func() string { return say.T("youtube.app") },
		Icon: "mdi:youtube",
		Open: func() { a.o.Shell.Push(a.view) },
	}
}

func (a *App) Screen(v shell.View) *navigation.Screen {
	if v != a.view {
		return nil
	}
	return &navigation.Screen{View: v, Build: a.build}
}

func (a *App) build(w *gogui.Window) gogui.View {
	vw, vh := w.WindowSize()
	reach := style.Reach()
	wide := vw >= vh
	content := a.page(w)
	top := reach * 0.4
	if !wide {
		top = cornerBand()
	}
	body := gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NewPadding(top, reach*pageSide, reach*0.4, reach*pageSide),
		Content: []gogui.View{content},
	})
	leave := widgets.LeaveCorner(a.o.UI.Presses.Pressable, "youtube-close", widgets.CornerSolid, 1,
		func() { a.o.Shell.Remove(a.view) })
	outer := gogui.ContainerCfg{
		Width:   float32(vw),
		Height:  float32(vh),
		Sizing:  gogui.FixedFixed,
		Padding: gogui.NoPadding,
		Spacing: gogui.NoSpacing,
		Color:   gogui.CurrentTheme().Cfg.ColorBackground,
	}
	if _, _, ok := a.viewer(); !ok && len(a.accounts.list()) == 0 {
		outer.Content = []gogui.View{body, leave}
		return gogui.Column(outer)
	}
	bar := a.tabs(wide)
	if wide {
		outer.Content = []gogui.View{bar, body, leave}
	} else {
		outer.Content = []gogui.View{body, bar, leave}
	}
	if a.refetching() {
		outer.Content = append(outer.Content, a.busy(w))
	}
	if wide {
		return gogui.Row(outer)
	}
	return gogui.Column(outer)
}

func cornerBand() float32 { return float32(widgets.CornerSide()) + 2*gogui.PadLarge }

func (a *App) page(w *gogui.Window) gogui.View {
	if sign, ok := a.accounts.state(); ok {
		return a.signInPage(w, sign)
	}
	key, s, ok := a.viewer()
	if !ok {
		return a.welcome(w)
	}
	a.mu.Lock()
	t := a.tab
	a.mu.Unlock()
	if t == accountsTab {
		return a.accountsPage(w)
	}
	if t == searchTab {
		return a.searchPage(w, key, s, a.width(w))
	}
	anonymous := key == schema.YouTubeAnonymous
	width := a.width(w)
	switch {
	case t == shortsTab:
		return a.shortsPage(w, shortsStream(key), s, width)
	case !anonymous && t == subscriptionsTab:
		return a.subscriptions(w, browsing(key, youtube.Subscriptions), s, width)
	case !anonymous && t == historyTab:
		return a.listed(w, browsing(key, youtube.History), s, width)
	case !anonymous && t == laterTab:
		return a.listed(w, browsing(key, youtube.WatchLater), s, width)
	}
	return a.home(w, browsing(key, youtube.Home), s, width)
}

func (a *App) refetching() bool {
	key, _, ok := a.viewer()
	if !ok {
		return false
	}
	a.mu.Lock()
	t := a.tab
	a.mu.Unlock()
	anonymous := key == schema.YouTubeAnonymous
	switch {
	case t == searchTab || t == accountsTab:
		return false
	case t == shortsTab:
		return a.feeds.refetching(shortsStream(key))
	case !anonymous && t == subscriptionsTab:
		return a.feeds.refetching(browsing(key, youtube.Subscriptions))
	case !anonymous && t == historyTab:
		return a.feeds.refetching(browsing(key, youtube.History))
	case !anonymous && t == laterTab:
		return a.feeds.refetching(browsing(key, youtube.WatchLater))
	}
	return a.feeds.refetching(browsing(key, youtube.Home))
}

func (a *App) busy(w *gogui.Window) gogui.View {
	vw, vh := w.WindowSize()
	return widgets.Floating(refreshZ, widgets.Spinner(vw, vh))
}

func (a *App) viewer() (string, *youtube.Session, bool) {
	if acc, ok := a.accounts.current(); ok {
		return acc.ID, a.accounts.session(acc), true
	}
	if a.accounts.isAnonymous() {
		return schema.YouTubeAnonymous, a.accounts.anonymousSession(), true
	}
	return "", nil, false
}

func (a *App) home(w *gogui.Window, k feedKey, s *youtube.Session, width float32) gogui.View {
	f := a.feeds.get(k, s)
	if !f.loaded {
		return a.state(f, "")
	}
	items := a.shelfItems(w, k, s, "youtube", f.shelves, width)
	if len(items) == 0 {
		if k.account == schema.YouTubeAnonymous {
			return note(say.T("youtube.unwatched"))
		}
		return note(say.T("youtube.empty"))
	}
	if f.loading {
		items = append(items, loadingItem)
	}
	seen := len(videos(f.shelves))
	return a.feedList(w, "youtube-home", items, func() { a.feeds.more(k, s, seen) })
}

func (a *App) shelfItems(
	w *gogui.Window,
	k feedKey,
	s *youtube.Session,
	prefix string,
	shelves []youtube.Shelf,
	width float32,
) []func() gogui.View {
	var items []func() gogui.View
	for i, sh := range shelves {
		id := prefix + "-shelf-" + strconv.Itoa(i)
		var load func() bool
		if sh.More != "" {
			load = func() bool { return a.feeds.moreShelf(k, s, i, len(sh.Videos)) }
		}
		if list := shorts(shelves[i : i+1]); len(list) == len(sh.Videos) {
			title := cmp.Or(sh.Title, say.T("youtube.shorts"))
			items = append(items, func() gogui.View {
				return a.shortShelf(w, id, title, list, width, load)
			})
			continue
		}
		items = append(items, func() gogui.View {
			return a.shelf(w, id, sh.Title, sh.Videos, width, load)
		})
	}
	return items
}

func (a *App) listed(w *gogui.Window, k feedKey, s *youtube.Session, width float32) gogui.View {
	f := a.feeds.get(k, s)
	list := videos(f.shelves)
	if len(list) == 0 {
		return a.state(f, say.T("youtube.empty"))
	}
	items := a.gridItems(w, "youtube-"+k.browse, list, width)
	if f.loading {
		items = append(items, loadingItem)
	}
	return a.feedList(w, "youtube-list-"+k.browse, items, func() { a.feeds.more(k, s, len(list)) })
}

func (a *App) subscriptions(
	w *gogui.Window,
	k feedKey,
	s *youtube.Session,
	width float32,
) gogui.View {
	f := a.feeds.get(k, s)
	all := videos(f.shelves)
	clips := slices.DeleteFunc(slices.Clone(all), isShort)
	quick := shorts(f.shelves)
	if len(clips) == 0 && len(quick) == 0 {
		return a.state(f, say.T("youtube.empty"))
	}
	var items []func() gogui.View
	if len(quick) > 0 {
		items = append(items, func() gogui.View {
			return a.shortShelf(
				w,
				"youtube-subscribed-shorts",
				say.T("youtube.shorts"),
				quick,
				width,
				nil,
			)
		})
	}
	items = append(items, a.gridItems(w, "youtube-subscribed", clips, width)...)
	if f.loading {
		items = append(items, loadingItem)
	}
	return a.feedList(
		w,
		"youtube-list-subscriptions",
		items,
		func() { a.feeds.more(k, s, len(all)) },
	)
}

func loadingItem() gogui.View { return gogui.Label(say.T("home.loading"), widgets.Secondary()) }

func (a *App) feedList(
	w *gogui.Window,
	id string,
	items []func() gogui.View,
	load func(),
) gogui.View {
	gap := gogui.CurrentTheme().Cfg.SpacingLarge
	list := gogui.VirtualList(gogui.VirtualListCfg{
		ID:            id,
		ItemCount:     len(items),
		Sizing:        gogui.FillFill,
		FocusDisabled: true,
		Padding:       gogui.NewPadding(0, style.Reach()*listSide, 0, 0),
		Color:         gogui.CurrentTheme().Cfg.ColorBackground,
		ItemView: func(i int, _ float32) gogui.View {
			if i == len(items)-1 && load != nil &&
				w.ScrollVerticalOffset(widgets.ScrollID(w, id)) < 0 {
				load()
			}
			return gogui.Column(gogui.ContainerCfg{
				Sizing:  gogui.FillFit,
				Padding: gogui.NewPadding(0, 0, gap, 0),
				Content: []gogui.View{items[i]()},
			})
		},
	})
	cfg := gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NoPadding,
		Content: []gogui.View{list},
	}
	if load != nil {
		cfg.AmendLayout = func(e gogui.EventCtx) {
			if hidden, ok := e.Window.ScrollOverflowY(
				widgets.ScrollID(e.Window, id),
			); ok &&
				hidden <= 0 {
				safe.Go("youtube fill", load)
			}
		}
	}
	return gogui.Column(cfg)
}

func inView(w *gogui.Window, leaf string, step, width float32) (int, int) {
	at := -w.ScrollHorizontalOffset(widgets.ScrollID(w, leaf))
	return int(at/step) - 1, int((at+width)/step) + 1
}

func (a *App) searchPage(
	w *gogui.Window,
	account string,
	s *youtube.Session,
	width float32,
) gogui.View {
	a.mu.Lock()
	draft, query := a.draft, a.query
	a.mu.Unlock()
	submit := func() {
		a.mu.Lock()
		a.query = strings.TrimSpace(a.draft)
		a.mu.Unlock()
		w.ClearFocus()
		a.redraw()
	}
	field := a.o.UI.Search(gogui.InputCfg{
		ID:          "youtube-search",
		Text:        draft,
		Placeholder: say.T("youtube.search"),
		Sizing:      gogui.FillFit,
		OnTextChanged: func(text string, e gogui.EventCtx) {
			a.mu.Lock()
			a.draft = text
			a.mu.Unlock()
			e.Window.InvalidateLayout()
		},
		OnTextCommit: func(_ string, _ gogui.InputCommitReason, _ gogui.EventCtx) { submit() },
	}, submit)
	views := []gogui.View{field}
	if query != "" {
		k := searching(account, query)
		f := a.feeds.get(k, s)
		if results := a.shelfItems(w, k, s, "youtube-result", f.shelves, width); len(results) > 0 {
			if f.loading {
				results = append(results, loadingItem)
			}
			seen := len(videos(f.shelves))
			views = append(views, a.feedList(w, "youtube-results", results, func() {
				a.feeds.more(k, s, seen)
			}))
		} else {
			views = append(views, a.state(f, say.T("youtube.empty")))
		}
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingLarge,
		Content: views,
	})
}

func (a *App) state(f feed, empty string) gogui.View {
	switch {
	case f.failed:
		return note(say.T("youtube.failed"))
	case !f.loaded:
		return note(say.T("home.loading"))
	}
	return note(empty)
}

func (a *App) shelf(
	w *gogui.Window,
	id, title string,
	list []youtube.Video,
	width float32,
	load func() bool,
) gogui.View {
	card := a.cardWidth(width, cardUnits)
	step := card + gogui.CurrentTheme().Cfg.SpacingMedium
	a.topUp(id, list, step, width, load)
	first, last := inView(w, id, step, width)
	cards := make([]gogui.View, 0, len(list))
	for i, v := range list {
		shown := i >= first && i <= last
		cards = append(cards, a.videoCard(w, id+"-"+strconv.Itoa(i), v, card, list, i, shown))
	}
	return a.row(id, title, cards, step, first, load)
}

func (a *App) shortShelf(
	w *gogui.Window,
	id, title string,
	list []youtube.Video,
	width float32,
	load func() bool,
) gogui.View {
	card := a.cardWidth(width, shortUnits)
	step := card + gogui.CurrentTheme().Cfg.SpacingMedium
	a.topUp(id, list, step, width, load)
	first, last := inView(w, id, step, width)
	cards := make([]gogui.View, 0, len(list))
	for i, v := range list {
		shown := i >= first && i <= last
		cards = append(cards, a.shortCard(w, id+"-"+strconv.Itoa(i), v, card, shown,
			func() { a.play(list, i) }))
	}
	return a.row(id, title, cards, step, first, load)
}

func (a *App) topUp(id string, list []youtube.Video, step, width float32, load func() bool) {
	if load == nil || len(list) == 0 || float32(len(list))*step > width {
		return
	}
	key := id + "/" + list[0].ID
	a.mu.Lock()
	done := a.topped[key]
	a.mu.Unlock()
	if done || !load() {
		return
	}
	a.mu.Lock()
	a.topped[key] = true
	a.mu.Unlock()
}

func (a *App) row(
	id, title string,
	cards []gogui.View,
	step float32,
	first int,
	load func() bool,
) gogui.View {
	head := gogui.CurrentTheme().Cfg.TextStyleDef
	head.Size *= 1.2
	var top []gogui.View
	if title != "" {
		top = append(top, gogui.Label(title, head))
	}
	bar := gogui.CurrentTheme().ScrollbarStyle
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingSmall,
		Content: append(top, gogui.Row(gogui.ContainerCfg{
			ID:         id,
			Sizing:     gogui.FillFit,
			Padding:    gogui.NewPadding(0, 0, bar.Size+2*bar.GapEdge, 0),
			Spacing:    gogui.SpacingMedium,
			Scrollable: true,
			ScrollMode: gogui.ScrollHorizontalOnly,
			Clip:       true,
			OnGesture:  a.rowGesture(load),
			OnScroll:   onScroll(step, first, load),
			Content:    cards,
		})),
	})
}

func (a *App) rowGesture(load func() bool) func(gogui.EventCtx) {
	return func(e gogui.EventCtx) {
		a.o.UI.HoldStill(e)
		if load == nil || e.Event == nil || e.Event.GestureType != gogui.GesturePan ||
			e.Event.GesturePhase != gogui.GesturePhaseChanged || e.Event.GestureDX >= 0 {
			return
		}
		if nearEnd(e.Window, e.Layout.Shape.ID) {
			load()
		}
	}
}

func onScroll(step float32, first int, load func() bool) func(gogui.EventCtx) {
	return func(e gogui.EventCtx) {
		if now, _ := inView(e.Window, e.Layout.Shape.ID, step, 0); now != first {
			e.Window.InvalidateLayout()
		}
		if load != nil && nearEnd(e.Window, e.Layout.Shape.ID) {
			load()
		}
	}
}

func nearEnd(w *gogui.Window, leaf string) bool {
	id := widgets.ScrollID(w, leaf)
	hidden, ok := w.ScrollOverflowX(id)
	return ok && hidden+w.ScrollHorizontalOffset(id) < style.Reach()*cardUnits
}

func (a *App) gridItems(
	w *gogui.Window,
	id string,
	list []youtube.Video,
	width float32,
) []func() gogui.View {
	per := columns(width, cardUnits)
	gap := gogui.CurrentTheme().Cfg.SpacingMedium
	card := (width - gap*float32(per-1)) / float32(per)
	return gridRows(per, len(list), func(i int) gogui.View {
		return a.videoCard(w, id+"-"+strconv.Itoa(i), list[i], card, list, i, true)
	})
}

func (a *App) shortsPage(w *gogui.Window, k feedKey, s *youtube.Session, width float32) gogui.View {
	f := a.feeds.get(k, s)
	list := shorts(f.shelves)
	if len(list) == 0 {
		return a.state(f, say.T("youtube.empty"))
	}
	seen := len(videos(f.shelves))
	load := func() bool { return a.feeds.more(k, s, seen) }
	play := func(i int) { a.playStream(k, s, list, i) }
	items := a.shortGridItems(w, list, width, play)
	if f.loading {
		items = append(items, loadingItem)
	}
	return a.feedList(w, "youtube-shorts", items, func() { load() })
}

func (a *App) shortGridItems(
	w *gogui.Window,
	list []youtube.Video,
	width float32,
	play func(int),
) []func() gogui.View {
	per := columns(width, shortGridUnits)
	gap := gogui.CurrentTheme().Cfg.SpacingMedium
	_, vh := w.WindowSize()
	if tall := (float32(vh) - style.Reach()*shortBare) / shortRows; tall > 0 {
		per = max(per, int((width+gap)/(tall*9/16+gap)))
	}
	card := (width - gap*float32(per-1)) / float32(per)
	return gridRows(per, len(list), func(i int) gogui.View {
		return a.shortCard(w, "youtube-shorts-"+strconv.Itoa(i), list[i], card, true,
			func() { play(i) })
	})
}

func gridRows(per, n int, card func(int) gogui.View) []func() gogui.View {
	var rows []func() gogui.View
	for start := 0; start < n; start += per {
		rows = append(rows, func() gogui.View {
			var cards []gogui.View
			for i := start; i < min(start+per, n); i++ {
				cards = append(cards, card(i))
			}
			return gogui.Row(gogui.ContainerCfg{
				Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
				Content: cards,
			})
		})
	}
	return rows
}

func (a *App) welcome(w *gogui.Window) gogui.View {
	reach := style.Reach()
	head := gogui.CurrentTheme().Cfg.TextStyleDef
	head.Size *= 1.6
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Spacing: gogui.SpacingLarge,
		Content: []gogui.View{
			a.mark(reach * 2.4),
			gogui.Label(say.T("youtube.app"), head),
			a.button("youtube-sign-in", say.T("youtube.signin"), true, a.accounts.signIn),
			a.button("youtube-anonymous", say.T("youtube.stay"), false, func() {
				a.accounts.stayAnonymous()
				a.open(homeTab)
			}),
		},
	})
}

func (a *App) signInPage(w *gogui.Window, s signing) gogui.View {
	page := widgets.SignIn{
		Mark:  a.mark(style.Reach() * 1.6),
		Title: say.T("youtube.signin"),
	}
	switch {
	case s.problem != "":
		page.Note = s.problem
		page.Actions = append(page.Actions, widgets.SignInAction{
			ID: "youtube-again", Label: say.T("youtube.again"), Primary: true,
			Do: a.accounts.signIn,
		})
	case s.asking:
		page.Note = say.T("youtube.asking")
	default:
		page.Go, page.Site, page.Enter = say.T("youtube.go"), activateSite, say.T("youtube.enter")
		page.Code, page.Waiting = s.code.User, say.T("youtube.waiting")
		page.Link = activateURL + url.QueryEscape(s.code.User)
	}
	if len(a.accounts.list()) > 0 || a.accounts.isAnonymous() || s.problem != "" {
		page.Actions = append(page.Actions, widgets.SignInAction{
			ID: "youtube-cancel", Label: say.T("common.actions.cancel"), Do: a.accounts.stopSigning,
		})
	}
	return a.o.UI.SignIn(w, page)
}

func (a *App) accountsPage(w *gogui.Window) gogui.View {
	reach := style.Reach()
	current, signed := a.accounts.current()
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	row := func(id string, face gogui.View, name string, chosen bool, action gogui.View, pick func()) gogui.View {
		cfg := gogui.ContainerCfg{
			ID:      "youtube-account-" + id,
			Sizing:  gogui.FillFit,
			Padding: gogui.PaddingMedium,
			Spacing: gogui.SpacingMedium,
			Radius:  gogui.RadiusLarge,
			VAlign:  gogui.VAlignMiddle,
		}
		st := body
		a.o.UI.Toolkit.Tab(&cfg, &st, widgets.Chosen(chosen))
		cfg.Content = []gogui.View{
			face,
			gogui.Row(gogui.ContainerCfg{
				Sizing: gogui.FillFit, Padding: gogui.NoPadding,
				Content: []gogui.View{gogui.Label(name, st)},
			}),
			action,
		}
		return a.o.UI.Toolkit.Pressable(gogui.Row, cfg, func(e gogui.EventCtx) {
			pick()
			a.open(homeTab)
			e.Consume()
		})
	}
	rows := []gogui.View{row(
		schema.YouTubeAnonymous,
		a.anonymousFace(reach*1.2),
		say.T("youtube.anonymous"),
		a.accounts.isAnonymous(),
		a.icon("youtube-reset", "mdi:restore", func() {
			a.accounts.reset()
			a.feeds.forget(schema.YouTubeAnonymous)
			a.redraw()
		}),
		a.accounts.stayAnonymous,
	)}
	for _, acc := range a.accounts.list() {
		id := acc.ID
		rows = append(rows, row(
			id,
			a.avatar(acc.Name, acc.Photo, reach*1.2),
			acc.Name,
			signed && id == current.ID,
			a.icon("youtube-signout-"+id, "mdi:logout", func() {
				a.accounts.remove(id)
				a.feeds.forget(id)
				a.redraw()
			}),
			func() { a.accounts.choose(id) },
		))
	}
	rows = append(rows, a.button("youtube-add", say.T("youtube.add"), false, a.accounts.signIn))
	return a.scroller("youtube-accounts", rows)
}

func (a *App) tabs(wide bool) gogui.View {
	reach := style.Reach()
	a.mu.Lock()
	open := a.tab
	a.mu.Unlock()
	glyph := gogui.CurrentTheme().TextStyleIconMedium
	glyph.Family = icons.Family
	glyph.Size = reach * 0.7
	small := widgets.Secondary()
	current, _ := a.accounts.current()
	anonymous := a.accounts.isAnonymous()
	key := func(
		id, icon string,
		face gogui.View,
		label string,
		chosen bool,
		do func(*gogui.Window),
	) gogui.View {
		cfg := gogui.ContainerCfg{
			ID:      "youtube-tab-" + id,
			Sizing:  gogui.FillFit,
			Padding: gogui.NewPadding(reach*0.25, reach*0.1, reach*0.25, reach*0.1),
			Spacing: gogui.SpacingSmall,
			HAlign:  gogui.HAlignCenter,
		}
		g, l := glyph, small
		a.o.UI.Toolkit.Tab(&cfg, &g, widgets.Chosen(chosen))
		l.Color = g.Color
		if face == nil {
			face = gogui.Label(icons.Glyph(icons.Named(icon)), g)
		}
		cfg.Content = []gogui.View{face, gogui.Label(label, l)}
		return a.o.UI.Toolkit.Pressable(gogui.Column, cfg, func(e gogui.EventCtx) {
			do(e.Window)
			e.Consume()
		})
	}
	var keys []gogui.View
	for _, t := range []struct {
		tab         tab
		id, icon, k string
		list        string
		signed      bool
	}{
		{searchTab, "search", "mdi:magnify", "youtube.search", "", false},
		{homeTab, "home", "mdi:home-variant-outline", "youtube.home", "youtube-home", false},
		{shortsTab, "shorts", "mdi:play-box-multiple-outline", "youtube.shorts", "youtube-shorts", false},
		{subscriptionsTab, "subscriptions", "mdi:youtube-subscription", "youtube.subscriptions",
			"youtube-list-subscriptions", true},
		{historyTab, "history", "mdi:history", "youtube.history", "youtube-list-" + youtube.History, true},
		{laterTab, "later", "mdi:clock-outline", "youtube.later", "youtube-list-" + youtube.WatchLater, true},
	} {
		if t.signed && anonymous {
			continue
		}
		icon, do := t.icon, func(*gogui.Window) { a.open(t.tab) }
		if open == t.tab && t.list != "" {
			icon, do = "mdi:refresh", func(w *gogui.Window) { a.reload(w, t.list) }
		}
		keys = append(keys, key(t.id, icon, nil, say.T(t.k), open == t.tab, do))
	}
	if wide {
		keys = append(
			keys,
			gogui.Column(gogui.ContainerCfg{Sizing: gogui.FillFill, Padding: gogui.NoPadding}),
		)
	}
	face := a.avatar(current.Name, current.Photo, reach*0.7)
	if anonymous {
		face = a.anonymousFace(reach * 0.7)
	}
	keys = append(keys, key(
		"accounts",
		"",
		face,
		say.T(
			"youtube.accounts",
		),
		open == accountsTab,
		func(*gogui.Window) { a.open(accountsTab) },
	))
	cfg := gogui.ContainerCfg{
		ID:      "youtube-tabs",
		Padding: gogui.PaddingSmall,
		Spacing: gogui.SpacingSmall,
		Color:   gogui.CurrentTheme().Cfg.ColorPanel,
		Content: keys,
	}
	if wide {
		cfg.Width, cfg.Sizing = reach*railUnits, gogui.FixedFill
		cfg.Padding = gogui.NewPadding(cornerBand(), gogui.PadSmall, gogui.PadSmall, gogui.PadSmall)
		return gogui.Column(cfg)
	}
	cfg.Sizing = gogui.FillFit
	return gogui.Row(cfg)
}

func (a *App) reload(w *gogui.Window, list string) {
	w.ScrollVerticalTo(widgets.ScrollID(w, list), 0)
	if key, _, ok := a.viewer(); ok {
		a.feeds.refresh(key)
	}
}

func (a *App) open(t tab) {
	a.mu.Lock()
	a.tab = t
	a.mu.Unlock()
	a.redraw()
}

func (a *App) play(list []youtube.Video, index int) {
	a.mu.Lock()
	a.began = time.Now()
	a.mu.Unlock()
	a.player.Play(list, index)
}

func (a *App) playStream(k feedKey, s *youtube.Session, list []youtube.Video, index int) {
	a.mu.Lock()
	a.began = time.Now()
	a.mu.Unlock()
	a.player.PlayStream(list, index, func(context.Context) []youtube.Video {
		return a.feeds.nextBatch(k, s)
	})
}

func (a *App) skips(ctx context.Context, id string) []sponsorblock.Segment {
	if a.o.Skip == nil {
		return nil
	}
	segs, err := sponsorblock.Segments(ctx, a.o.Client, sponsorblock.URL, id, a.o.Skip())
	if err != nil {
		return nil
	}
	return segs
}

func (a *App) related(ctx context.Context, id string) []youtube.Video {
	_, s, ok := a.viewer()
	if !ok {
		return nil
	}
	return a.feeds.related(ctx, s, id)
}

func (a *App) width(w *gogui.Window) float32 {
	vw, vh := w.WindowSize()
	reach := style.Reach()
	if vw >= vh {
		return float32(vw) - reach*(railUnits+2*pageSide+listSide)
	}
	return float32(vw) - reach*(2*pageSide+listSide)
}

func (a *App) cardWidth(width, units float32) float32 {
	per := float32(columns(width, units)) + peek
	gap := gogui.CurrentTheme().Cfg.SpacingMedium
	return (width - gap*per) / per
}

func columns(width, units float32) int {
	return max(2, int(width/(style.Reach()*units)))
}

func (a *App) mark(side float32) gogui.View {
	st := gogui.CurrentTheme().TextStyleIconMedium
	st.Family, st.Size = icons.Family, side
	st.Color = widgets.Color(a.o.UI.Palette().Danger)
	return gogui.Label(icons.Glyph(icons.Named("mdi:youtube")), st)
}

func (a *App) button(id, label string, primary bool, do func()) gogui.View {
	cfg := gogui.ContainerCfg{
		ID:     id,
		Sizing: gogui.FitFit,
		Padding: gogui.NewPadding(
			gogui.PadMedium,
			gogui.PadLarge*2,
			gogui.PadMedium,
			gogui.PadLarge*2,
		),
		Radius: gogui.RadiusLarge,
		HAlign: gogui.HAlignCenter,
	}
	st := gogui.CurrentTheme().Cfg.TextStyleDef
	a.o.UI.Toolkit.Tab(&cfg, &st, widgets.Chosen(primary))
	cfg.Content = []gogui.View{gogui.Label(label, st)}
	return a.o.UI.Toolkit.Pressable(gogui.Row, cfg, func(e gogui.EventCtx) {
		do()
		e.Consume()
	})
}

func (a *App) icon(id, name string, do func()) gogui.View {
	st := gogui.CurrentTheme().TextStyleIconMedium
	st.Family, st.Size = icons.Family, style.Reach()*0.7
	st.Color = widgets.Secondary().Color
	return a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
		ID:      id,
		Padding: gogui.PaddingSmall,
		Radius:  gogui.RadiusLarge,
		Content: []gogui.View{gogui.Label(icons.Glyph(icons.Named(name)), st)},
	}, func(e gogui.EventCtx) {
		do()
		e.Consume()
	})
}

func (a *App) scroller(id string, views []gogui.View) gogui.View {
	return gogui.Column(gogui.ContainerCfg{
		ID: id, Sizing: gogui.FillFill, Padding: gogui.NewPadding(0, style.Reach()*listSide, 0, 0),
		Spacing: gogui.SpacingLarge, Scrollable: true, ScrollMode: gogui.ScrollVerticalOnly,
		OnGesture: a.o.UI.HoldStill, Content: views,
	})
}

func (a *App) download(ctx context.Context, url string) ([]byte, error) {
	if a.o.Client == nil || !strings.HasPrefix(url, "http") {
		return nil, fmt.Errorf("youtubeapp: no way to download %q", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtubeapp: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, artLimit))
}

func (a *App) redraw() {
	if a.o.Redraw != nil {
		a.o.Redraw()
	}
}

func note(text string) gogui.View {
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Content: []gogui.View{gogui.Label(text, widgets.Secondary())},
	})
}

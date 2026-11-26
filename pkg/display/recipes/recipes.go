package recipes

import (
	"image"
	"strconv"
	"strings"
	"sync"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"
	_ "golang.org/x/image/webp"

	"github.com/ygelfand/libcountertop/pkg/display/apps"
	"github.com/ygelfand/libcountertop/pkg/display/covers"
	"github.com/ygelfand/libcountertop/pkg/display/icons"
	"github.com/ygelfand/libcountertop/pkg/display/menu"
	"github.com/ygelfand/libcountertop/pkg/display/navigation"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/mealie"
	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/voice"
)

const (
	cardUnits   = 3.6
	typePause   = 600 * time.Millisecond
	cookTimeout = 90 * time.Minute
	pictures    = 160
)

type Options struct {
	UI      *widgets.UI
	Shell   *shell.Shell
	Kitchen *mealie.Kitchen
	Voice   Voice
	Redraw  func()
}

type Voice interface {
	Listen(h voice.Hook) (cancel func())
	Allowed(name string) bool
}

type Browse struct{}

func (*Browse) Covers() bool { return true }

func (*Browse) Timeout() time.Duration { return shell.AppTimeout }

type Dish struct{}

func (*Dish) Covers() bool { return true }

func (*Dish) Timeout() time.Duration { return shell.AppTimeout }

type Cooking struct{}

func (*Cooking) Covers() bool { return true }

func (*Cooking) Timeout() time.Duration { return cookTimeout }

type App struct {
	o        Options
	browse   *Browse
	dish     *Dish
	cook     *Cooking
	pictures *covers.Covers

	mu        sync.Mutex
	typed     string
	query     string
	favorites bool
	slug      string
	servings  map[string]float64
	ticked    map[string]map[int]bool
	pause     *time.Timer
	hush      func()
	pager     *widgets.Pager
	stageSize image.Point
	shown     int
	shownSlug string
	panAxis   panAxis
}

type panAxis int

const (
	panUndecided panAxis = iota
	panSideways
	panVertical
)

func New(o Options) *App {
	a := &App{
		o:        o,
		browse:   &Browse{},
		dish:     &Dish{},
		cook:     &Cooking{},
		servings: map[string]float64{},
		ticked:   map[string]map[int]bool{},
		pager:    widgets.NewPager("recipes-cook-slide"),
	}
	a.pictures = covers.New(covers.Options{
		Prefix: "recipes",
		Get:    o.Kitchen.Picture,
		Redraw: a.redraw,
		Keep:   pictures,
	})
	o.Kitchen.Asked.Listen(a.asked)
	o.Shell.Changed.Listen(func(shell.Change) { a.listen(o.Shell.Visible(a.cook)) })
	return a
}

func (a *App) App() apps.App {
	return apps.App{
		ID:   schema.AppRecipes,
		Name: func() string { return say.T("recipes.app") },
		Icon: "mdi:chef-hat",
		Open: a.open,
	}
}

func (a *App) open() {
	a.o.Kitchen.Refresh()
	a.o.Shell.Push(a.browse)
}

func (a *App) asked(s mealie.Show) {
	switch s.Kind {
	case mealie.ShowBrowse:
		a.mu.Lock()
		a.typed, a.query, a.favorites = s.Query, s.Query, false
		a.mu.Unlock()
		a.o.Shell.Push(a.browse)
	case mealie.ShowRecipe:
		a.show(s.Slug)
	case mealie.ShowCook:
		a.mu.Lock()
		a.slug = s.Slug
		a.mu.Unlock()
		a.o.Shell.Push(a.cook)
	}
	a.redraw()
}

func (a *App) show(slug string) {
	a.mu.Lock()
	a.slug = slug
	a.mu.Unlock()
	a.o.Kitchen.Refresh()
	a.o.Shell.Push(a.dish)
}

func (a *App) Screen(v shell.View) *navigation.Screen {
	switch v {
	case a.browse:
		return &navigation.Screen{
			Title:    say.T("recipes.app"),
			View:     v,
			Build:    a.browsing,
			Fixed:    true,
			Settings: func() { a.o.Shell.OpenSettings(a.Page()) },
		}
	case a.dish:
		return &navigation.Screen{
			Title: a.title(), Head: a.dishHead, View: v, Build: a.dishView, Fixed: true,
		}
	case a.cook:
		return &navigation.Screen{
			Title: a.title(), Head: a.cookHead, View: v, Build: a.cooking, Fixed: true,
		}
	}
	return nil
}

func (a *App) title() string {
	a.mu.Lock()
	slug := a.slug
	a.mu.Unlock()
	if s, ok := a.o.Kitchen.Session(); ok && s.Recipe.Slug == slug {
		return s.Recipe.Name
	}
	if r, err, done := a.o.Kitchen.Recipe(slug); done && err == nil {
		return r.Name
	}
	return say.T("recipes.app")
}

func (a *App) browsing(w *gogui.Window) gogui.View {
	k := a.o.Kitchen
	if !k.Configured() {
		return a.o.UI.Row("recipes-setup", "recipes", menu.Row{
			Label: say.T("recipes.setup"), Hint: say.T("recipes.setup.hint"), Kind: menu.Chevron,
		}, func(int) { a.o.Shell.OpenSettings(a.Page()) }, false)
	}
	a.mu.Lock()
	typed, query, favorites := a.typed, a.query, a.favorites
	a.mu.Unlock()
	head := gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{
			a.o.UI.Search(gogui.InputCfg{
				ID:          "recipes-search",
				Text:        typed,
				Placeholder: say.T("recipes.search"),
				Sizing:      gogui.FillFit,
				OnTextChanged: func(text string, e gogui.EventCtx) {
					a.typing(text)
					e.Window.InvalidateLayout()
				},
			}, a.commit),
			a.chip("recipes-all", "mdi:silverware-fork-knife", say.T("recipes.all"), !favorites,
				func() { a.filter(false) }),
			a.chip("recipes-favorites", "mdi:heart", say.T("recipes.favorites"), favorites,
				func() { a.filter(true) }),
		},
	})
	views := []gogui.View{head}
	shelf := k.List(query, favorites)
	switch st := k.Status(); {
	case !shelf.Loaded && (st == mealie.Refused || st == mealie.Unreachable):
		views = append(views, note(st.Says()))
	case !shelf.Loaded:
		views = append(views, note(say.T("home.loading")))
	case shelf.Err != nil && len(shelf.Items) == 0:
		views = append(views, note(k.Status().Says()))
	case len(shelf.Items) == 0 && favorites:
		views = append(views, note(say.T("recipes.nofavorites")))
	case len(shelf.Items) == 0:
		views = append(views, note(say.T("recipes.none")))
	default:
		body := []gogui.View{a.grid(w, shelf.Items, widgets.PageWidth(w))}
		if shelf.More {
			body = append(body, gogui.Row(gogui.ContainerCfg{
				Sizing: gogui.FillFit, Padding: gogui.NoPadding, HAlign: gogui.HAlignCenter,
				Content: []gogui.View{a.chip("recipes-more", "mdi:chevron-down",
					say.T("recipes.more"), false, func() { k.More(query, favorites) })},
			}))
		}
		views = append(views, a.scroller("recipes-grid", body))
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: views,
	})
}

func (a *App) filter(favorites bool) {
	a.mu.Lock()
	a.favorites = favorites
	a.mu.Unlock()
	a.redraw()
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

func (a *App) grid(w *gogui.Window, items []mealie.Summary, width float32) gogui.View {
	per := max(2, int(width/(style.Reach()*cardUnits)))
	gap := gogui.CurrentTheme().Cfg.SpacingMedium
	side := (width - style.Reach()*0.4 - gap*float32(per-1)) / float32(per)
	var rows []gogui.View
	for start := 0; start < len(items); start += per {
		var cards []gogui.View
		for i, it := range items[start:min(start+per, len(items))] {
			cards = append(cards, a.card(w, "recipes-card-"+strconv.Itoa(start+i), it, side))
		}
		rows = append(rows, gogui.Row(gogui.ContainerCfg{
			Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
			Content: cards,
		}))
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingLarge,
		Content: rows,
	})
}

func (a *App) card(w *gogui.Window, id string, it mealie.Summary, side float32) gogui.View {
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	name := []gogui.View{}
	room := side
	if a.o.Kitchen.Favorite(it.ID) {
		heart := badge(body.Size)
		room -= body.Size * 1.4
		name = append(name, heart)
	}
	name = append([]gogui.View{gogui.Label(widgets.Ellipsize(w, it.Name, body, room), body)},
		name...)
	return a.o.UI.Toolkit.Pressable(gogui.Column, gogui.ContainerCfg{
		ID: id, Width: side, Sizing: gogui.FixedFit, Padding: gogui.NoPadding,
		Spacing: gogui.SpacingSmall, Radius: gogui.RadiusMedium,
		Content: []gogui.View{
			a.picture(it, side, side*0.75),
			gogui.Row(gogui.ContainerCfg{
				Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
				VAlign: gogui.VAlignMiddle, Content: name,
			}),
			gogui.Label(widgets.Ellipsize(w, facts(it), widgets.Secondary(), side),
				widgets.Secondary()),
		},
	}, func(e gogui.EventCtx) {
		a.show(it.Slug)
		e.Consume()
	})
}

var heartRed = gogui.Color{R: 0xf2, G: 0x4b, B: 0x6a, A: 0xff}

func badge(size float32) gogui.View {
	st := gogui.CurrentTheme().TextStyleIconMedium
	st.Family, st.Size, st.Color = icons.Family, size, heartRed
	return gogui.Label(icons.Glyph(icons.Named("mdi:heart")), st)
}

func facts(s mealie.Summary) string {
	var parts []string
	if d := s.Total(); d > 0 {
		parts = append(parts, duration(d))
	}
	if s.Rating != nil && *s.Rating > 0 {
		parts = append(parts, "★ "+strconv.FormatFloat(*s.Rating, 'f', -1, 64))
	}
	if len(parts) == 0 && len(s.Categories) > 0 {
		parts = append(parts, s.Categories[0].Name)
	}
	return strings.Join(parts, " · ")
}

func duration(d time.Duration) string {
	minutes := mealie.Minutes(d)
	if minutes < 60 {
		return say.F("recipes.minutes", map[string]any{"N": minutes})
	}
	if minutes%60 == 0 {
		return say.F("recipes.hours", map[string]any{"N": minutes / 60})
	}
	return say.F("recipes.hours_minutes", map[string]any{"H": minutes / 60, "M": minutes % 60})
}

func (a *App) picture(s mealie.Summary, w, h float32) gogui.View {
	radius := gogui.RadiusPx(min(w, h) * 0.06)
	if url := a.o.Kitchen.PictureURL(s, mealie.Small); url != "" {
		if src := a.pictures.Src(url, int(w)); src != "" {
			return gogui.Column(gogui.ContainerCfg{
				Width: w, Height: h, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
				Radius: radius, Clip: true, VAlign: gogui.VAlignMiddle,
				Content: []gogui.View{gogui.Image(gogui.ImageCfg{Src: src, Width: w, Height: w})},
			})
		}
	}
	st := gogui.CurrentTheme().TextStyleIconMedium
	st.Family, st.Size = icons.Family, min(w, h)*0.4
	st.Color = widgets.Secondary().Color
	return gogui.Column(gogui.ContainerCfg{
		Width: w, Height: h, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
		Radius: radius, Color: gogui.CurrentTheme().Cfg.ColorPanel,
		HAlign: gogui.HAlignCenter, VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{gogui.Label(icons.Glyph(icons.Named("mdi:pot-steam")), st)},
	})
}

func (a *App) chip(id, icon, label string, on bool, do func()) gogui.View {
	reach := style.Reach()
	glyph := gogui.CurrentTheme().TextStyleIconMedium
	glyph.Family, glyph.Size = icons.Family, reach*0.5
	cfg := gogui.ContainerCfg{
		ID:      id,
		Sizing:  gogui.FitFit,
		Padding: gogui.NewPadding(reach*0.18, reach*0.32, reach*0.18, reach*0.32),
		Spacing: gogui.SpacingSmall,
		VAlign:  gogui.VAlignMiddle,
	}
	st := gogui.CurrentTheme().Cfg.TextStyleDef
	a.o.UI.Toolkit.Tab(&cfg, &st, widgets.Chosen(on))
	glyph.Color = st.Color
	content := []gogui.View{gogui.Label(icons.Glyph(icons.Named(icon)), glyph)}
	if label != "" {
		content = append(content, gogui.Label(label, st))
	}
	cfg.Content = content
	return a.o.UI.Toolkit.Pressable(gogui.Row, cfg, func(e gogui.EventCtx) {
		do()
		e.Consume()
	})
}

func (a *App) scroller(id string, views []gogui.View) gogui.View {
	return gogui.Column(gogui.ContainerCfg{
		ID: id, Sizing: gogui.FillFill, Padding: gogui.NewPadding(0, style.Reach()*0.4, 0, 0),
		Spacing: gogui.SpacingLarge, Scrollable: true, OnGesture: a.o.UI.HoldStill,
		Content: views,
	})
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

func wrap(text string, st gogui.TextStyle) gogui.View {
	return gogui.Text(gogui.TextCfg{Text: text, TextStyle: st, Mode: gogui.TextModeWrap})
}

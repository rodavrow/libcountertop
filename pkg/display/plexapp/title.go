package plexapp

import (
	"cmp"
	"log/slog"
	"strconv"
	"strings"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/plex"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/say"
)

func (a *App) current() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.trail) == 0 {
		return ""
	}
	return a.trail[len(a.trail)-1]
}

func (a *App) titleHead(w *gogui.Window) gogui.View {
	name := say.T("plex.app")
	if it, loaded, err := a.o.Plex.Item(a.current()); loaded && err == nil {
		name = heading(it)
	}
	st := gogui.CurrentTheme().TextStyleDisplay
	room := widgets.PageWidth(w) - style.Reach()*1.5
	return gogui.Label(widgets.Ellipsize(w, name, st, room), st)
}

func (a *App) titled(w *gogui.Window) gogui.View {
	it, loaded, err := a.o.Plex.Item(a.current())
	switch {
	case !loaded:
		return note(say.T("home.loading"))
	case err != nil:
		return note(say.T("plex.unreachable"))
	}
	width := widgets.PageWidth(w)
	vw, vh := w.WindowSize()
	side := width * 0.3
	if vw < vh {
		side = width * 0.45
	}
	h := side * tall
	if it.Type == "episode" {
		h = side * wide
	}
	if it.Type == "artist" || it.Type == "album" {
		h = side
	}
	about := a.about(w, it)
	top := gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingLarge,
		Content: []gogui.View{a.poster(it, it.Thumb, side, h), about},
	})
	if vw < vh {
		top = gogui.Column(gogui.ContainerCfg{
			Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingLarge,
			Content: []gogui.View{a.poster(it, it.Thumb, side, h), about},
		})
	}
	views := []gogui.View{top}
	if !it.Playable() {
		views = append(views, a.inside(w, it))
	}
	page := a.scroller("plex-title-"+it.RatingKey, views)
	if !a.isStarting(it) {
		return page
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding,
		Content: []gogui.View{page, gogui.Column(gogui.ContainerCfg{
			Float: true, FloatAnchor: gogui.FloatMiddleCenter, FloatTieOff: gogui.FloatMiddleCenter,
			Padding: gogui.NoPadding,
			Content: []gogui.View{widgets.SpinnerGlyph(float32(min(vw, vh)) / spinnerPart)},
		})},
	})
}

func (a *App) isStarting(it plex.Item) bool {
	a.mu.Lock()
	asked := a.starting == it.RatingKey
	a.mu.Unlock()
	if asked {
		return true
	}
	now, ok := a.o.Plex.Player().Starting()
	return ok && now.RatingKey == it.RatingKey
}

func (a *App) about(w *gogui.Window, it plex.Item) gogui.View {
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	views := []gogui.View{}
	if it.Type == "episode" {
		views = append(views, gogui.Label(
			say.F("plex.episode", map[string]any{"S": it.Season0, "E": it.Episode})+" · "+it.Title,
			body))
	}
	var facts []string
	if it.Year > 0 {
		facts = append(facts, strconv.Itoa(it.Year))
	}
	if it.Duration > 0 {
		facts = append(facts, length(it.Duration))
	}
	if d := detail(
		it,
	); d != "" && it.Type != "episode" &&
		!strings.Contains(strings.Join(facts, " "), d) {
		facts = append(facts, d)
	}
	if len(facts) > 0 {
		views = append(views, gogui.Label(strings.Join(facts, " · "), widgets.Secondary()))
	}
	if it.Summary != "" {
		views = append(views, wrap(it.Summary, body))
	}
	if it.Playable() {
		views = append(views, a.actions(it))
	}
	if mark := a.watchedChip(it); mark != nil && !it.Playable() {
		views = append(views, mark)
	}
	a.mu.Lock()
	failed := a.failed == it.RatingKey
	a.mu.Unlock()
	if broke, ok := a.o.Plex.Player().Failed(); ok && broke.RatingKey == it.RatingKey {
		failed = true
	}
	if failed {
		views = append(views, wrap(say.T("plex.cantplay"), widgets.Secondary()))
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: views,
	})
}

func (a *App) actions(it plex.Item) gogui.View {
	var chips []gogui.View
	if it.Offset > 0 && it.Progress() < 1 {
		chips = append(chips,
			a.chip("plex-resume", "mdi:play", say.F("plex.resume",
				map[string]any{"Time": length(it.Offset)}), true, func() { a.play(it, true) }),
			a.chip("plex-restart", "mdi:restart", say.T("plex.fromstart"), false,
				func() { a.play(it, false) }),
		)
	} else {
		chips = append(chips, a.chip("plex-play", "mdi:play", say.T("plex.play"), true,
			func() { a.play(it, false) }))
	}
	if sub := a.subtitleChip(it); sub != nil {
		chips = append(chips, sub)
	}
	if mark := a.watchedChip(it); mark != nil {
		chips = append(chips, mark)
	}
	return gogui.Wrap(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		VAlign: gogui.VAlignMiddle, Content: chips,
	})
}

func watched(it plex.Item) bool {
	if it.Playable() {
		return it.Viewed && it.Offset == 0
	}
	return it.Children > 0 && it.Unwatched == 0
}

func (a *App) watchedChip(it plex.Item) gogui.View {
	switch it.Type {
	case "movie", "episode", "show", "season":
	default:
		return nil
	}
	seen := watched(it)
	icon, label := "mdi:eye-check-outline", say.T("plex.mark_watched")
	if seen {
		icon, label = "mdi:eye-off-outline", say.T("plex.mark_unwatched")
	}
	return a.chip("plex-watched-"+it.RatingKey, icon, label, false, func() {
		safe.Go("plex watched", func() {
			if err := a.o.Plex.SetWatched(it, !seen); err != nil {
				slog.Warn("plex watched", "item", it.RatingKey, "err", err)
			}
		})
	})
}

const offValue = "off"

func (a *App) subtitleChip(it plex.Item) gogui.View {
	if !it.Playable() || len(it.Subtitles) == 0 {
		return nil
	}
	off := plex.Subtitle{Part: it.Subtitles[0].Part, Title: say.T("common.states.off")}
	choices := append([]plex.Subtitle{off}, it.Subtitles...)
	options := make([]gogui.SelectOption, 0, len(choices))
	current := offValue
	for _, s := range choices {
		value := cmp.Or(s.ID, offValue)
		options = append(options, gogui.NewSelectOption(
			say.F("plex.subtitles", map[string]any{"Name": s.Title}), value))
		if s.Selected {
			current = value
		}
	}
	reach := style.Reach()
	return gogui.Select(gogui.SelectCfg{
		ID:        "plex-subtitles-" + it.RatingKey,
		A11YCfg:   gogui.A11YCfg{A11YLabel: say.F("plex.subtitles", map[string]any{"Name": ""})},
		TextStyle: gogui.CurrentTheme().Cfg.TextStyleDef,
		Options:   options,
		Selected:  []string{current},
		Padding:   gogui.NewPadding(reach*0.18, reach*0.32, reach*0.18, reach*0.32),
		Radius:    gogui.RadiusLarge,
		Sizing:    gogui.FitFit,
		OnSelect: func(values []string, e gogui.EventCtx) {
			if len(values) == 0 {
				return
			}
			for _, s := range choices {
				if cmp.Or(s.ID, offValue) == values[0] {
					safe.Go("plex subtitles", func() {
						if err := a.o.Plex.SetSubtitle(it, s); err != nil {
							slog.Warn("plex subtitles", "item", it.RatingKey, "err", err)
						}
					})
				}
			}
			e.Window.InvalidateLayout()
		},
	})
}

func (a *App) inside(w *gogui.Window, it plex.Item) gogui.View {
	kids, loaded, err := a.o.Plex.Children(it.RatingKey)
	switch {
	case !loaded:
		return note(say.T("home.loading"))
	case err != nil:
		return note(say.T("plex.unreachable"))
	case len(kids) == 0:
		return note(say.T("plex.empty"))
	}
	if it.Type == "show" || it.Type == "artist" {
		return a.grid(w, "plex-inside-"+it.RatingKey, kids, widgets.PageWidth(w))
	}
	rows := make([]gogui.View, 0, len(kids))
	for i, k := range kids {
		rows = append(rows, a.line(w, "plex-line-"+itoa(i), k))
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		Content: rows,
	})
}

func (a *App) line(w *gogui.Window, id string, k plex.Item) gogui.View {
	side := style.Reach() * 2.6
	h := side * wide
	if k.Type == "track" {
		h = 0
	}
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	quiet := widgets.Secondary()
	room := widgets.PageWidth(w) - side - style.Reach()
	label := k.Title
	if k.Episode > 0 {
		label = itoa(k.Episode) + ". " + k.Title
	}
	text := []gogui.View{gogui.Label(widgets.Ellipsize(w, label, body, room), body)}
	if k.Duration > 0 {
		text = append(text, gogui.Label(length(k.Duration), quiet))
	}
	content := []gogui.View{}
	if h > 0 {
		content = append(content, a.poster(k, k.Thumb, side, h))
	}
	content = append(content, gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		Content: text,
	}))
	return a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
		ID: id, Sizing: gogui.FillFit, Padding: gogui.PaddingSmall, Spacing: gogui.SpacingMedium,
		Radius: gogui.RadiusMedium, VAlign: gogui.VAlignMiddle, Content: content,
	}, func(e gogui.EventCtx) {
		if k.Type == "track" {
			a.play(k, false)
		} else {
			a.show(k)
		}
		e.Consume()
	})
}

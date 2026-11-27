package plexapp

import (
	"log/slog"
	"strings"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/icons"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/plex"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/say"
)

const (
	rowsView    = "rows"
	allView     = "all"
	genresView  = "genres"
	letterUnits = 0.9
	tileUnits   = 4.5
)

func (a *App) library(w *gogui.Window, section string) gogui.View {
	a.mu.Lock()
	view := a.view
	a.mu.Unlock()
	rows, rowsLoaded, _ := a.o.Plex.Rows(section)
	noRows := rowsLoaded && len(rows) == 0
	if view == "" {
		view = rowsView
	}
	if view == rowsView && noRows {
		view = allView
	}
	cats, _, _ := a.o.Plex.Categories(section)
	var keys []gogui.View
	if !noRows {
		keys = append(keys, a.chip("plex-view-rows", "mdi:star-outline", say.T("plex.recommended"),
			view == rowsView, func() { a.look(rowsView) }))
	}
	keys = append(keys, a.chip("plex-view-all", "mdi:view-grid-outline", say.T("plex.library"),
		view == allView, func() { a.look(allView) }))
	if len(cats) > 0 {
		keys = append(
			keys,
			a.chip("plex-view-genres", "mdi:shape-outline", say.T("plex.categories"),
				view == genresView, func() { a.look(genresView) }),
		)
	}
	tabs := gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: keys,
	})
	var body gogui.View
	switch view {
	case allView:
		body = a.everything(w, section)
	case genresView:
		body = a.categories(w, section, cats)
	default:
		body = a.recommended(w, section)
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: []gogui.View{tabs, body},
	})
}

func (a *App) categories(w *gogui.Window, section string, cats []plex.Category) gogui.View {
	width := a.bodyWidth(w) - style.Reach()*0.4
	gap := gogui.CurrentTheme().Cfg.SpacingMedium
	per := max(2, int(width/(style.Reach()*tileUnits)))
	side := (width - gap*float32(per-1)) / float32(per)
	h := side * wide
	tiles := make([]gogui.View, 0, len(cats))
	for _, c := range cats {
		tiles = append(tiles, a.tile(c, side, h))
	}
	return a.scroller("plex-genres-"+section, []gogui.View{gogui.Wrap(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: tiles,
	})})
}

func (a *App) tile(c plex.Category, w, h float32) gogui.View {
	layers := []gogui.View{}
	if url := a.o.Plex.Tinted(c.Art, c.Blend, int(w), int(h)); url != "" {
		if src := a.pictures.Src(url, int(w)); src != "" {
			layers = append(layers, gogui.Image(gogui.ImageCfg{Src: src, Width: w, Height: h}))
		}
	}
	st := gogui.CurrentTheme().TextStyleTitle
	st.Color = gogui.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	layers = append(layers, gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.PaddingSmall,
		HAlign: gogui.HAlignCenter, VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{gogui.Label(c.Title, st)},
	}))
	return a.o.UI.Toolkit.Pressable(gogui.Stack, gogui.ContainerCfg{
		ID: "plex-genre-" + c.Genre, Width: w, Height: h, Sizing: gogui.FixedFixed,
		Padding: gogui.NoPadding, Radius: gogui.RadiusPx(min(w, h) * 0.05), Clip: true,
		Color: gogui.CurrentTheme().Cfg.ColorPanel, Content: layers,
	}, func(e gogui.EventCtx) {
		a.mu.Lock()
		a.listing.Genre, a.genre, a.view = c.Genre, c.Title, allView
		a.mu.Unlock()
		a.redraw()
		e.Consume()
	})
}

func (a *App) look(view string) {
	a.mu.Lock()
	a.view = view
	a.mu.Unlock()
	a.redraw()
}

func (a *App) recommended(w *gogui.Window, section string) gogui.View {
	rows, loaded, err := a.o.Plex.Rows(section)
	switch {
	case !loaded:
		return widgets.Spinner(int(a.bodyWidth(w)), int(style.Reach()*cardUnits*tall))
	case err != nil && len(rows) == 0:
		return note(say.T("plex.unreachable"))
	case len(rows) == 0:
		return note(say.T("plex.empty"))
	}
	views := make([]gogui.View, 0, len(rows))
	for i, h := range rows {
		views = append(views, a.hubRow(w, "plex-row-"+section+"-"+itoa(i), h))
	}
	return a.scroller("plex-rows-"+section, views)
}

func (a *App) seeAll(h plex.Hub) {
	a.mu.Lock()
	a.row = &h
	a.mu.Unlock()
	a.redraw()
}

func (a *App) seeing(w *gogui.Window, h plex.Hub) gogui.View {
	head := gogui.CurrentTheme().Cfg.TextStyleDef
	head.Size *= 1.2
	top := gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{
			a.chip("plex-row-back", "mdi:arrow-left", "", false, func() {
				a.mu.Lock()
				a.row = nil
				a.mu.Unlock()
				a.redraw()
			}),
			gogui.Label(h.Title, head),
		},
	})
	items, loaded, err := a.o.Plex.SeeAll(h.Identifier)
	var body gogui.View
	switch {
	case !loaded:
		body = widgets.Spinner(int(a.bodyWidth(w)), int(style.Reach()*cardUnits*tall))
	case err != nil && len(items) == 0:
		body = note(say.T("plex.unreachable"))
	case len(items) == 0:
		body = note(say.T("plex.empty"))
	default:
		body = a.cards(w, "plex-seeall-"+h.Identifier, len(items), a.bodyWidth(w),
			func(i int) (plex.Item, bool) { return items[i], true })
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: []gogui.View{top, body},
	})
}

func (a *App) everything(w *gogui.Window, section string) gogui.View {
	a.mu.Lock()
	q := a.listing
	a.mu.Unlock()
	_, total, loaded, err := a.o.Plex.Page(section, q, 0)
	sorts, _, _ := a.o.Plex.Sorts(section)
	bar := a.listBar(section, q, sorts, total, loaded)
	var body gogui.View
	switch {
	case !loaded:
		body = widgets.Spinner(int(a.bodyWidth(w)), int(style.Reach()*cardUnits*tall))
	case err != nil && total == 0:
		body = note(say.T("plex.unreachable"))
	case total == 0:
		body = note(say.T("plex.empty"))
	default:
		body = a.pagedGrid(w, section, q, total, sorts)
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: []gogui.View{bar, body},
	})
}

func (a *App) listBar(
	section string,
	q plex.Query,
	sorts []plex.Sort,
	total int,
	loaded bool,
) gogui.View {
	chips := make([]gogui.View, 0, 6)
	if current, down, ok := currentSort(q, sorts); ok {
		options := make([]gogui.SelectOption, 0, len(sorts))
		for _, s := range sorts {
			options = append(options, gogui.NewSelectOption(
				say.F("plex.sort", map[string]any{"Name": s.Title}), s.Key))
		}
		reach := style.Reach()
		chips = append(chips, gogui.Select(gogui.SelectCfg{
			ID: "plex-sort-" + section,
			A11YCfg: gogui.A11YCfg{
				A11YLabel: say.F("plex.sort", map[string]any{"Name": current.Title}),
			},
			TextStyle: gogui.CurrentTheme().Cfg.TextStyleDef,
			Options:   options,
			Selected:  []string{current.Key},
			Padding:   gogui.NewPadding(reach*0.18, reach*0.32, reach*0.18, reach*0.32),
			Radius:    gogui.RadiusLarge,
			Sizing:    gogui.FitFit,
			OnSelect: func(values []string, e gogui.EventCtx) {
				for _, s := range sorts {
					if len(values) > 0 && s.Key == values[0] {
						a.sortBy(s, false, false)
					}
				}
				e.Window.InvalidateLayout()
			},
		}))
		if current.Desc != "" {
			icon := "mdi:arrow-up"
			if down {
				icon = "mdi:arrow-down"
			}
			chips = append(chips, a.chip("plex-sort-direction", icon, "", false,
				func() { a.sortBy(current, true, down) }))
		}
	}
	if q.Genre != "" {
		a.mu.Lock()
		name := a.genre
		a.mu.Unlock()
		chips = append(chips, a.chip("plex-genre-clear", "mdi:close", name, true, func() {
			a.mu.Lock()
			a.listing.Genre, a.genre = "", ""
			a.mu.Unlock()
			a.redraw()
		}))
	}
	chips = append(chips,
		a.chip("plex-unwatched", "mdi:eye-off-outline", say.T("plex.unwatched_only"), q.Unwatched,
			func() {
				a.mu.Lock()
				a.listing.Unwatched = !a.listing.Unwatched
				a.mu.Unlock()
				a.redraw()
			}),
	)
	if loaded && total > 0 {
		chips = append(chips,
			gogui.Label(say.F("plex.titles", map[string]any{"N": total}), widgets.Secondary()),
			a.chip("plex-shuffle", "mdi:shuffle-variant", say.T("plex.shuffle"), false,
				func() { a.shuffle(section, q, total) }),
		)
	}
	bar := gogui.CurrentTheme().ScrollbarStyle
	return gogui.Row(gogui.ContainerCfg{
		ID: "plex-list-bar", Sizing: gogui.FillFit, Spacing: gogui.SpacingMedium,
		Padding:    gogui.NewPadding(0, 0, bar.Size+2*bar.GapEdge, 0),
		VAlign:     gogui.VAlignMiddle,
		Scrollable: true, ScrollMode: gogui.ScrollHorizontalOnly, Clip: true,
		OnGesture: a.o.UI.HoldStill, Content: chips,
	})
}

func currentSort(q plex.Query, sorts []plex.Sort) (plex.Sort, bool, bool) {
	for _, s := range sorts {
		switch {
		case q.Sort == "" && s.Letters, q.Sort != "" && q.Sort == s.Key:
			return s, false, true
		case q.Sort != "" && q.Sort == s.Desc:
			return s, true, true
		}
	}
	if q.Sort == "" && len(sorts) > 0 {
		return sorts[0], sorts[0].Default == "desc", true
	}
	return plex.Sort{}, false, false
}

func (a *App) sortBy(s plex.Sort, on, down bool) {
	next := s.Key
	switch {
	case on && !down && s.Desc != "":
		next = s.Desc
	case !on && s.Default == "desc" && s.Desc != "":
		next = s.Desc
	}
	a.mu.Lock()
	a.listing.Sort = next
	a.mu.Unlock()
	a.redraw()
}

func (a *App) shuffle(section string, q plex.Query, total int) {
	safe.Go("plex shuffle", func() {
		it, err := a.o.Plex.Random(section, q, total)
		if err != nil {
			slog.Warn("plex shuffle", "section", section, "err", err)
			return
		}
		a.show(it)
		if it.Playable() {
			a.play(it, false)
		}
	})
}

func (a *App) pagedGrid(
	w *gogui.Window,
	section string,
	q plex.Query,
	total int,
	sorts []plex.Sort,
) gogui.View {
	id := "plex-grid-" + section
	width := a.bodyWidth(w)
	letters := a.letterBar(w, section, q, sorts, id, width)
	if letters != nil {
		width -= style.Reach() * letterUnits
	}
	size := plex.PageSize()
	grid := a.cards(w, id, total, width, func(i int) (plex.Item, bool) {
		items, _, loaded, _ := a.o.Plex.Page(section, q, i/size)
		if !loaded || i%size >= len(items) {
			return plex.Item{}, false
		}
		return items[i%size], true
	})
	if letters == nil {
		return grid
	}
	return gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.NoSpacing,
		Content: []gogui.View{
			gogui.Column(gogui.ContainerCfg{
				Width: width, Sizing: gogui.FixedFill, Padding: gogui.NoPadding,
				Content: []gogui.View{grid},
			}),
			letters,
		},
	})
}

func (a *App) letterBar(
	w *gogui.Window,
	section string,
	q plex.Query,
	sorts []plex.Sort,
	grid string,
	width float32,
) gogui.View {
	if q.Unwatched || q.Genre != "" {
		return nil
	}
	titled := false
	for _, s := range sorts {
		if s.Letters && (q.Sort == "" || q.Sort == s.Key) {
			titled = true
		}
	}
	if !titled {
		return nil
	}
	letters, loaded, _ := a.o.Plex.Letters(section)
	if !loaded || len(letters) == 0 {
		return nil
	}
	per, _ := columns(width - style.Reach()*letterUnits)
	reach := style.Reach()
	st := widgets.Secondary()
	keys := make([]gogui.View, 0, len(letters))
	at := 0
	for _, l := range letters {
		row := at / per
		at += l.Size
		keys = append(keys, a.o.UI.Toolkit.Pressable(gogui.Column, gogui.ContainerCfg{
			ID: "plex-letter-" + l.Key, Width: reach * letterUnits, Sizing: gogui.FixedFill,
			Padding: gogui.NoPadding, HAlign: gogui.HAlignCenter, VAlign: gogui.VAlignMiddle,
			Content: []gogui.View{gogui.Label(strings.ToUpper(l.Title), st)},
		}, func(e gogui.EventCtx) {
			e.Window.ScrollToIndex(widgets.ScrollID(e.Window, grid), row)
			e.Consume()
		}))
	}
	return gogui.Column(gogui.ContainerCfg{
		Width: reach * letterUnits, Sizing: gogui.FixedFill, Padding: gogui.NoPadding,
		Content: keys,
	})
}

func columns(width float32) (int, float32) {
	per := max(2, int(width/(style.Reach()*cardUnits)))
	gap := gogui.CurrentTheme().Cfg.SpacingMedium
	return per, (width - style.Reach()*0.4 - gap*float32(per-1)) / float32(per)
}

func (a *App) cards(
	w *gogui.Window,
	id string,
	n int,
	width float32,
	at func(int) (plex.Item, bool),
) gogui.View {
	per, side := columns(width)
	gap := gogui.CurrentTheme().Cfg.SpacingLarge
	return gogui.VirtualList(gogui.VirtualListCfg{
		ID:            id,
		ItemCount:     (n + per - 1) / per,
		Sizing:        gogui.FillFill,
		FocusDisabled: true,
		Padding:       gogui.NewPadding(0, style.Reach()*0.4, 0, 0),
		Color:         gogui.CurrentTheme().Cfg.ColorBackground,
		ItemView: func(row int, _ float32) gogui.View {
			cards := make([]gogui.View, 0, per)
			for i := row * per; i < min((row+1)*per, n); i++ {
				if it, ok := at(i); ok {
					cards = append(cards, a.card(w, id+"-"+itoa(i), it, side))
				} else {
					cards = append(cards, blank(side))
				}
			}
			return gogui.Row(gogui.ContainerCfg{
				Sizing: gogui.FillFit, Padding: gogui.NewPadding(0, 0, gap, 0),
				Spacing: gogui.SpacingMedium, Content: cards,
			})
		},
	})
}

func blank(side float32) gogui.View {
	quiet := widgets.Secondary()
	return gogui.Column(gogui.ContainerCfg{
		Width: side, Sizing: gogui.FixedFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		Content: []gogui.View{
			gogui.Column(gogui.ContainerCfg{
				Width:   side,
				Height:  side * tall,
				Sizing:  gogui.FixedFixed,
				Padding: gogui.NoPadding,
				Radius: gogui.RadiusPx(
					side * tall * 0.05,
				),
				Color: gogui.CurrentTheme().Cfg.ColorPanel,
			}),
			gogui.Label(" ", gogui.CurrentTheme().Cfg.TextStyleDef),
			gogui.Label(" ", quiet),
		},
	})
}

func (a *App) seeAllKey(h plex.Hub) gogui.View {
	if h.Identifier == "" {
		return nil
	}
	st := widgets.Secondary()
	glyph := gogui.CurrentTheme().TextStyleIconMedium
	glyph.Family, glyph.Size, glyph.Color = icons.Family, style.Reach()*0.45, st.Color
	return a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
		ID: "plex-seeall-key-" + h.Identifier, Sizing: gogui.FitFit, Padding: gogui.PaddingSmall,
		Spacing: gogui.SpacingSmall, VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{
			gogui.Label(say.T("plex.see_all"), st),
			gogui.Label(icons.Glyph(icons.Named("mdi:chevron-right")), glyph),
		},
	}, func(e gogui.EventCtx) {
		a.seeAll(h)
		e.Consume()
	})
}

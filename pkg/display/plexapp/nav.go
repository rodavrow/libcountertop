package plexapp

import (
	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/icons"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/plex"
	"github.com/ygelfand/libcountertop/pkg/say"
)

const (
	railUnits = 3.0
	pageSide  = 0.5
	refreshZ  = 1
)

func cornerBand() float32 { return float32(widgets.CornerSide()) + 2*gogui.PadLarge }

func (a *App) build(w *gogui.Window) gogui.View {
	vw, vh := w.WindowSize()
	reach := style.Reach()
	wide := vw >= vh
	top := reach * 0.4
	if !wide {
		top = cornerBand()
	}
	body := gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NewPadding(top, reach*pageSide, reach*0.4, reach*pageSide),
		Content: []gogui.View{a.browsing(w)},
	})
	leave := widgets.LeaveCorner(a.o.UI.Presses.Pressable, "plex-close", widgets.CornerSolid, 1,
		func() { a.o.Shell.Remove(a.browse) })
	outer := gogui.ContainerCfg{
		Width:   float32(vw),
		Height:  float32(vh),
		Sizing:  gogui.FixedFixed,
		Padding: gogui.NoPadding,
		Spacing: gogui.NoSpacing,
		Color:   gogui.CurrentTheme().Cfg.ColorBackground,
	}
	if a.o.Plex.Status() != plex.Ready {
		outer.Content = []gogui.View{body, leave}
		return gogui.Column(outer)
	}
	if wide {
		outer.Content = []gogui.View{a.rail(w, true), body, leave}
	} else {
		outer.Content = []gogui.View{body, a.rail(w, false), leave}
	}
	a.mu.Lock()
	tab := a.tab
	a.mu.Unlock()
	if a.refreshing(tab) {
		outer.Content = append(outer.Content, widgets.Floating(refreshZ, widgets.Spinner(vw, vh)))
	}
	if wide {
		return gogui.Row(outer)
	}
	return gogui.Column(outer)
}

func (a *App) bodyWidth(w *gogui.Window) float32 {
	vw, vh := w.WindowSize()
	reach := style.Reach()
	width := float32(vw) - 2*reach*pageSide
	if vw >= vh && a.o.Plex.Status() == plex.Ready {
		width -= reach * railUnits
	}
	return width
}

func (a *App) rail(w *gogui.Window, wide bool) gogui.View {
	reach := style.Reach()
	a.mu.Lock()
	open := a.tab
	a.mu.Unlock()
	side := reach * railUnits
	libraries := []gogui.View{}
	sections, _, _ := a.o.Plex.Sections()
	for _, sc := range sections {
		libraries = append(libraries, a.railKey(w, "plex-tab-"+sc.Key, sectionGlyph(sc.Type),
			sc.Title, open == sc.Key, side, a.tabPress(open, sc.Key)))
	}
	list := gogui.ContainerCfg{
		ID: "plex-libraries", Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		Scrollable: true, Clip: true, OnGesture: a.o.UI.HoldStill, Content: libraries,
	}
	keys := []gogui.View{
		a.railKey(w, "plex-tab-search", "mdi:magnify", say.T("plex.search"),
			open == searchTab, side, func(*gogui.Window) { a.choose(searchTab) }),
		a.railKey(w, "plex-tab-home", "mdi:home-variant-outline", say.T("plex.home"),
			open == homeTab, side, a.tabPress(open, homeTab)),
	}
	settings := a.railKey(w, "plex-settings", "mdi:cog-outline", say.T("settings.title"),
		false, side, func(*gogui.Window) { a.o.Shell.OpenSettings(a.Page()) })
	cfg := gogui.ContainerCfg{
		ID:      "plex-tabs",
		Padding: gogui.PaddingSmall,
		Spacing: gogui.SpacingSmall,
		Color:   gogui.CurrentTheme().Cfg.ColorPanel,
	}
	if wide {
		list.Sizing, list.ScrollMode = gogui.FillFill, gogui.ScrollVerticalOnly
		cfg.Width, cfg.Sizing = side, gogui.FixedFill
		cfg.Padding = gogui.NewPadding(cornerBand(), gogui.PadSmall, gogui.PadSmall, gogui.PadSmall)
		cfg.Content = append(keys, divider(true), gogui.Column(list), divider(true), settings)
		return gogui.Column(cfg)
	}
	list.Sizing, list.ScrollMode = gogui.FillFit, gogui.ScrollHorizontalOnly
	cfg.Sizing = gogui.FillFit
	cfg.Content = append(keys, divider(false), gogui.Row(list), divider(false), settings)
	return gogui.Row(cfg)
}

func divider(across bool) gogui.View {
	t := gogui.CurrentTheme().Cfg
	line := gogui.ContainerCfg{
		Width:   1,
		Height:  1,
		Padding: gogui.NoPadding,
		Color: gogui.RGBA(
			t.TextStyleDef.Color.R,
			t.TextStyleDef.Color.G,
			t.TextStyleDef.Color.B,
			0x30,
		),
	}
	if across {
		line.Sizing = gogui.FillFixed
	} else {
		line.Sizing = gogui.FixedFill
	}
	return gogui.Row(line)
}

func (a *App) railKey(
	w *gogui.Window,
	id, icon, label string,
	chosen bool,
	side float32,
	do func(*gogui.Window),
) gogui.View {
	reach := style.Reach()
	if chosen && id != "plex-tab-search" {
		icon = "mdi:refresh"
	}
	cfg := gogui.ContainerCfg{
		ID:      id,
		Width:   side - 2*gogui.PadSmall,
		Sizing:  gogui.FixedFit,
		Padding: gogui.NewPadding(reach*0.25, reach*0.1, reach*0.25, reach*0.1),
		Spacing: gogui.SpacingSmall,
		HAlign:  gogui.HAlignCenter,
	}
	glyph := gogui.CurrentTheme().TextStyleIconMedium
	glyph.Family, glyph.Size = icons.Family, reach*0.7
	small := widgets.Secondary()
	a.o.UI.Toolkit.Tab(&cfg, &glyph, widgets.Chosen(chosen))
	small.Color = glyph.Color
	room := cfg.Width - 2*reach*0.1
	cfg.Content = []gogui.View{
		gogui.Label(icons.Glyph(icons.Named(icon)), glyph),
		gogui.Label(widgets.Ellipsize(w, label, small, room), small),
	}
	return a.o.UI.Toolkit.Pressable(gogui.Column, cfg, func(e gogui.EventCtx) {
		do(e.Window)
		e.Consume()
	})
}

func (a *App) tabPress(open, tab string) func(*gogui.Window) {
	if open != tab {
		return func(*gogui.Window) { a.choose(tab) }
	}
	return func(w *gogui.Window) { a.reload(w, tab) }
}

func (a *App) reload(w *gogui.Window, tab string) {
	a.mu.Lock()
	a.row = nil
	a.mu.Unlock()
	if tab == homeTab {
		w.ScrollVerticalTo(widgets.ScrollID(w, "plex-home"), 0)
		a.o.Plex.RefreshHome()
		return
	}
	w.ScrollVerticalTo(widgets.ScrollID(w, "plex-rows-"+tab), 0)
	w.ScrollVerticalTo(widgets.ScrollID(w, "plex-grid-"+tab), 0)
	a.o.Plex.RefreshLibrary(tab)
}

func (a *App) refreshing(tab string) bool {
	if tab == homeTab {
		return a.o.Plex.HomeRefreshing()
	}
	return tab != searchTab && a.o.Plex.LibraryRefreshing(tab)
}

func sectionGlyph(kind string) string {
	switch kind {
	case "show":
		return "mdi:television-classic"
	case "artist":
		return "mdi:music"
	case "photo":
		return "mdi:image-multiple-outline"
	}
	return "mdi:movie-open-outline"
}

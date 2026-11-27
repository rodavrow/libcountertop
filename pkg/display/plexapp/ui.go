package plexapp

import (
	"strconv"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/icons"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/plex"
	"github.com/ygelfand/libcountertop/pkg/say"
)

const (
	cardUnits = 3.2
	tall      = 1.5
	wide      = 0.5625
)

var progressGold = gogui.Color{R: 0xe5, G: 0xa0, B: 0x0d, A: 0xff}

func (a *App) hubRow(w *gogui.Window, id string, h plex.Hub) gogui.View {
	side := style.Reach() * cardUnits
	cards := make([]gogui.View, 0, len(h.Items))
	for i, it := range h.Items {
		cards = append(cards, a.card(w, id+"-"+itoa(i), it, side))
	}
	head := gogui.CurrentTheme().Cfg.TextStyleDef
	head.Size *= 1.2
	bar := gogui.CurrentTheme().ScrollbarStyle
	top := []gogui.View{gogui.Label(h.Title, head)}
	if key := a.seeAllKey(h); key != nil {
		top = append(top, key)
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		Content: []gogui.View{
			gogui.Row(gogui.ContainerCfg{
				Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
				VAlign: gogui.VAlignMiddle, Content: top,
			}),
			gogui.Row(gogui.ContainerCfg{
				ID: id, Sizing: gogui.FillFit, Spacing: gogui.SpacingMedium,
				Padding:    gogui.NewPadding(0, 0, bar.Size+2*bar.GapEdge, 0),
				Scrollable: true, ScrollMode: gogui.ScrollHorizontalOnly, Clip: true,
				OnGesture: a.o.UI.HoldStill, Content: cards,
			}),
		},
	})
}

func (a *App) grid(w *gogui.Window, id string, items []plex.Item, width float32) gogui.View {
	per, side := columns(width)
	var rows []gogui.View
	for start := 0; start < len(items); start += per {
		var cards []gogui.View
		for i, it := range items[start:min(start+per, len(items))] {
			cards = append(cards, a.card(w, id+"-"+itoa(start+i), it, side))
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

func (a *App) card(w *gogui.Window, id string, it plex.Item, side float32) gogui.View {
	h := side * tall
	if it.Type == "clip" {
		h = side * wide
	}
	if it.Type == "artist" || it.Type == "album" || it.Type == "track" {
		h = side
	}
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	quiet := widgets.Secondary()
	return a.o.UI.Toolkit.Pressable(gogui.Column, gogui.ContainerCfg{
		ID: id, Width: side, Sizing: gogui.FixedFit, Padding: gogui.NoPadding,
		Spacing: gogui.SpacingSmall, Radius: gogui.RadiusMedium,
		Content: []gogui.View{
			a.poster(it, it.Poster, side, h),
			gogui.Label(widgets.Ellipsize(w, heading(it), body, side), body),
			gogui.Label(widgets.Ellipsize(w, detail(it), quiet, side), quiet),
		},
	}, func(e gogui.EventCtx) {
		a.show(it)
		e.Consume()
	})
}

func (a *App) poster(it plex.Item, path string, w, h float32) gogui.View {
	radius := gogui.RadiusPx(min(w, h) * 0.05)
	var layers []gogui.View
	if url := a.o.Plex.Picture(path, int(w), int(h)); url != "" {
		if src := a.pictures.Src(url, int(w)); src != "" {
			layers = append(layers, gogui.Image(gogui.ImageCfg{Src: src, Width: w, Height: h}))
		}
	}
	if len(layers) == 0 {
		st := gogui.CurrentTheme().TextStyleIconMedium
		st.Family, st.Size = icons.Family, min(w, h)*0.35
		st.Color = widgets.Secondary().Color
		layers = append(layers, gogui.Column(gogui.ContainerCfg{
			Sizing: gogui.FillFill, Padding: gogui.NoPadding,
			HAlign: gogui.HAlignCenter, VAlign: gogui.VAlignMiddle,
			Content: []gogui.View{gogui.Label(icons.Glyph(icons.Named(glyph(it))), st)},
		}))
	}
	if p := it.Progress(); p > 0 {
		bar := max(h*0.03, 3)
		layers = append(layers, gogui.Column(gogui.ContainerCfg{
			Sizing: gogui.FillFill, Padding: gogui.NoPadding, VAlign: gogui.VAlignBottom,
			Content: []gogui.View{gogui.Row(gogui.ContainerCfg{
				Width: w, Height: bar, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
				Color: gogui.RGBA(0, 0, 0, 140),
				Content: []gogui.View{gogui.Row(gogui.ContainerCfg{
					Width: w * float32(p), Height: bar, Sizing: gogui.FixedFixed,
					Padding: gogui.NoPadding, Color: progressGold,
				})},
			})},
		}))
	}
	return gogui.Stack(gogui.ContainerCfg{
		Width: w, Height: h, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
		Radius: radius, Clip: true, Color: gogui.CurrentTheme().Cfg.ColorPanel,
		Content: layers,
	})
}

func glyph(it plex.Item) string {
	switch it.Type {
	case "show", "season", "episode":
		return "mdi:television-classic"
	case "artist", "album", "track":
		return "mdi:music"
	}
	return "mdi:movie-open"
}

func heading(it plex.Item) string {
	if it.Type == "episode" && it.Show != "" {
		return it.Show
	}
	return it.Title
}

func detail(it plex.Item) string {
	switch it.Type {
	case "episode":
		return say.F(
			"plex.episode",
			map[string]any{"S": it.Season0, "E": it.Episode},
		) + " · " + it.Title
	case "season":
		if it.Unwatched > 0 {
			return say.F("plex.unwatched", map[string]any{"N": it.Unwatched})
		}
		return say.F("plex.episodes", map[string]any{"N": it.Children})
	case "show":
		if it.Unwatched > 0 {
			return say.F("plex.unwatched", map[string]any{"N": it.Unwatched})
		}
	case "album", "track":
		return it.Show
	}
	if it.Year > 0 {
		return strconv.Itoa(it.Year)
	}
	return ""
}

func length(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m < 60 {
		return say.F("plex.minutes", map[string]any{"N": m})
	}
	if m%60 == 0 {
		return say.F("plex.hours", map[string]any{"N": m / 60})
	}
	return say.F("plex.hours_minutes", map[string]any{"H": m / 60, "M": m % 60})
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
	var content []gogui.View
	if icon != "" {
		content = append(content, gogui.Label(icons.Glyph(icons.Named(icon)), glyph))
	}
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
		Spacing: gogui.SpacingLarge, Scrollable: true, ScrollMode: gogui.ScrollVerticalOnly,
		OnGesture: a.o.UI.HoldStill, Content: views,
	})
}

func note(text string) gogui.View {
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.PaddingLarge,
		HAlign: gogui.HAlignCenter, VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{wrap(text, widgets.Secondary())},
	})
}

func wrap(text string, st gogui.TextStyle) gogui.View {
	return gogui.Text(gogui.TextCfg{Text: text, TextStyle: st, Mode: gogui.TextModeWrap})
}

func itoa(n int) string { return strconv.Itoa(n) }

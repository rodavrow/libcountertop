package youtubeapp

import (
	"cmp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/icons"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/youtube"
	"github.com/ygelfand/libcountertop/pkg/say"
)

const (
	thumbBase = "https://i.ytimg.com/vi/"
	thumbFile = "/mqdefault.jpg"
	barUnits  = 0.14
	pillAlpha = 210
)

var (
	pillFill = gogui.Color{A: pillAlpha}
	pillInk  = gogui.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	barTrack = gogui.Color{R: 0x30, G: 0x30, B: 0x30, A: 0xe0}
	shade    = gogui.Color{A: 0x80}
)

func thumbURL(id string) string { return thumbBase + id + thumbFile }

func (a *App) videoCard(
	w *gogui.Window,
	id string,
	v youtube.Video,
	width float32,
	list []youtube.Video,
	index int,
	shown bool,
) gogui.View {
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	quiet := widgets.Secondary()
	lines := []gogui.View{
		a.thumb(v, width, width*9/16, shown),
		gogui.Label(widgets.Ellipsize(w, v.Title, body, width), body),
	}
	if v.Channel != "" {
		lines = append(lines, gogui.Label(widgets.Ellipsize(w, v.Channel, quiet, width), quiet))
	}
	if v.Detail != "" {
		lines = append(lines, gogui.Label(widgets.Ellipsize(w, v.Detail, quiet, width), quiet))
	}
	return a.o.UI.Toolkit.Pressable(gogui.Column, gogui.ContainerCfg{
		ID:      id,
		Width:   width,
		Sizing:  gogui.FixedFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingSmall,
		Radius:  gogui.RadiusMedium,
		Content: lines,
	}, func(e gogui.EventCtx) {
		a.play(list, index)
		e.Consume()
	})
}

func (a *App) thumb(v youtube.Video, cw, ch float32, shown bool) gogui.View {
	t := gogui.CurrentTheme().Cfg
	var layers []gogui.View
	if shown {
		layers = append(layers, a.picture(thumbURL(v.ID), cw, ch, cw))
	}
	switch {
	case v.Kind == youtube.Broadcast:
		layers = append(layers, pill(say.T("player.live"), widgets.Color(a.o.UI.Palette().Danger)))
	case v.Length > 0:
		layers = append(layers, pill(clockText(v.Length), pillFill))
	}
	if v.Watched > 0 {
		done := cw * float32(min(v.Watched, 100)) / 100
		bar := style.Reach() * barUnits
		layers = append(layers, corner(gogui.HAlignLeft, 0, gogui.Row(gogui.ContainerCfg{
			Width:   cw,
			Height:  bar,
			Sizing:  gogui.FixedFixed,
			Padding: gogui.NoPadding,
			Color:   barTrack,
			Content: []gogui.View{gogui.Row(gogui.ContainerCfg{
				Width:   done,
				Height:  bar,
				Sizing:  gogui.FixedFixed,
				Padding: gogui.NoPadding,
				Color:   widgets.Color(a.o.UI.Palette().Danger),
			})},
		})))
	}
	layers = append(layers, a.starting(v, cw, ch)...)
	return gogui.Stack(gogui.ContainerCfg{
		Width:   cw,
		Height:  ch,
		Sizing:  gogui.FixedFixed,
		Padding: gogui.NoPadding,
		Radius:  gogui.RadiusPx(cw * 0.04),
		Clip:    true,
		Color:   t.ColorPanel,
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Content: layers,
	})
}

func (a *App) shortCard(
	w *gogui.Window,
	id string,
	v youtube.Video,
	width float32,
	shown bool,
	play func(),
) gogui.View {
	height := width * 16 / 9
	var picture []gogui.View
	if shown {
		picture = append(picture, a.upright(v, width, height))
		if v.Title == "" {
			if named := a.titles.named(v); named.Title != "" {
				picture = append(picture, caption(w, named.Title, width))
			}
		}
	}
	picture = append(picture, a.starting(v, width, height)...)
	art := gogui.Stack(gogui.ContainerCfg{
		Width:   width,
		Height:  height,
		Sizing:  gogui.FixedFixed,
		Padding: gogui.NoPadding,
		Radius:  gogui.RadiusPx(width * 0.06),
		Clip:    true,
		Color:   gogui.CurrentTheme().Cfg.ColorPanel,
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Content: picture,
	})
	lines := []gogui.View{art}
	if v.Title != "" {
		lines = append(lines, twoLines(w, v.Title, gogui.CurrentTheme().Cfg.TextStyleDef, width)...)
	}
	if under := cmp.Or(v.Detail, v.Channel); under != "" {
		quiet := widgets.Secondary()
		lines = append(lines, gogui.Label(widgets.Ellipsize(w, under, quiet, width), quiet))
	}
	return a.o.UI.Toolkit.Pressable(gogui.Column, gogui.ContainerCfg{
		ID:      id,
		Width:   width,
		Sizing:  gogui.FixedFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingSmall,
		Radius:  gogui.RadiusPx(width * 0.06),
		Content: lines,
	}, func(e gogui.EventCtx) {
		play()
		e.Consume()
	})
}

func (a *App) starting(v youtube.Video, width, height float32) []gogui.View {
	s, ok := a.player.Starting()
	if !ok || s.ID != v.ID {
		return nil
	}
	if a.spinning.CompareAndSwap(false, true) {
		time.AfterFunc(widgets.SpinStep, func() {
			a.spinning.Store(false)
			a.redraw()
		})
	}
	return []gogui.View{gogui.Column(gogui.ContainerCfg{
		Width:   width,
		Height:  height,
		Sizing:  gogui.FixedFixed,
		Padding: gogui.NoPadding,
		Color:   shade,
		Content: []gogui.View{widgets.Spinner(int(width), int(height))},
	})}
}

func (a *App) upright(v youtube.Video, width, height float32) gogui.View {
	url, aspect := thumbURL(v.ID), float32(16)/9
	if p := v.Thumbnail; p.URL != "" && p.Width > 0 && p.Height > 0 {
		url, aspect = p.URL, float32(p.Width)/float32(p.Height)
	}
	iw, ih := height*aspect, height
	if aspect < width/height {
		iw, ih = width, width/aspect
	}
	return a.picture(url, iw, ih, width)
}

func twoLines(w *gogui.Window, text string, st gogui.TextStyle, width float32) []gogui.View {
	words := strings.Fields(text)
	n := 0
	for n < len(words) && w.TextWidth(strings.Join(words[:n+1], " "), st) <= width {
		n++
	}
	if n == 0 || n == len(words) {
		return []gogui.View{gogui.Label(widgets.Ellipsize(w, text, st, width), st)}
	}
	return []gogui.View{
		gogui.Label(strings.Join(words[:n], " "), st),
		gogui.Label(widgets.Ellipsize(w, strings.Join(words[n:], " "), st, width), st),
	}
}

func (a *App) picture(url string, iw, ih, frame float32) gogui.View {
	if src := a.thumbs.Src(url, int(iw)); src != "" {
		return gogui.Image(gogui.ImageCfg{Src: src, Width: iw, Height: ih})
	}
	st := gogui.CurrentTheme().TextStyleIconMedium
	st.Family, st.Size = icons.Family, frame*0.18
	st.Color = widgets.Secondary().Color
	return gogui.Label(icons.Glyph(icons.Named("mdi:youtube")), st)
}

func pill(text string, fill gogui.Color) gogui.View {
	st := widgets.Secondary()
	st.Color = pillInk
	reach := style.Reach()
	return corner(gogui.HAlignRight, reach*0.12, gogui.Row(gogui.ContainerCfg{
		Padding: gogui.NewPadding(reach*0.04, reach*0.12, reach*0.04, reach*0.12),
		Radius:  gogui.RadiusSmall,
		Color:   fill,
		Content: []gogui.View{gogui.Label(text, st)},
	}))
}

func caption(w *gogui.Window, title string, width float32) gogui.View {
	st := gogui.CurrentTheme().Cfg.TextStyleDef
	st.Color = pillInk
	pad := style.Reach() * 0.12
	return corner(gogui.HAlignLeft, 0, gogui.Column(gogui.ContainerCfg{
		Width:   width,
		Sizing:  gogui.FixedFit,
		Padding: gogui.NewPadding(pad, pad, pad, pad),
		Color:   pillFill,
		Content: twoLines(w, title, st, width-2*pad),
	}))
}

func corner(side gogui.HorizontalAlign, inset float32, v gogui.View) gogui.View {
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NewPadding(inset, inset, inset, inset),
		HAlign:  side,
		VAlign:  gogui.VAlignBottom,
		Content: []gogui.View{v},
	})
}

func (a *App) avatar(name, photo string, side float32) gogui.View {
	if src := a.faces.Src(photo, int(side)); src != "" {
		return gogui.Circle(gogui.ContainerCfg{
			Width:   side,
			Height:  side,
			Sizing:  gogui.FixedFixed,
			Padding: gogui.NoPadding,
			Clip:    true,
			Content: []gogui.View{gogui.Image(gogui.ImageCfg{Src: src, Width: side, Height: side})},
		})
	}
	st := gogui.CurrentTheme().Cfg.TextStyleDef
	st.Size, st.Color = side*0.5, pillInk
	return gogui.Circle(gogui.ContainerCfg{
		Width: side, Height: side, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
		Color:  widgets.Color(a.o.UI.Palette().Danger),
		HAlign: gogui.HAlignCenter, VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{gogui.Label(initial(name), st)},
	})
}

func (a *App) anonymousFace(side float32) gogui.View {
	st := gogui.CurrentTheme().TextStyleIconMedium
	st.Family, st.Size, st.Color = icons.Family, side*0.6, pillInk
	return gogui.Circle(gogui.ContainerCfg{
		Width: side, Height: side, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
		Color:  widgets.Color(a.o.UI.Palette().Danger),
		HAlign: gogui.HAlignCenter, VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{gogui.Label(icons.Glyph(icons.Named("mdi:incognito")), st)},
	})
}

func initial(name string) string {
	r, _ := utf8.DecodeRuneInString(strings.TrimSpace(name))
	if r == utf8.RuneError {
		return "?"
	}
	return string(unicode.ToUpper(r))
}

func clockText(d time.Duration) string {
	s := int(d / time.Second)
	h, m, sec := s/3600, s/60%60, s%60
	if h > 0 {
		return strconv.Itoa(h) + ":" + two(m) + ":" + two(sec)
	}
	return strconv.Itoa(m) + ":" + two(sec)
}

func two(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

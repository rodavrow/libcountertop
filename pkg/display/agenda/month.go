package agenda

import (
	"fmt"
	"strconv"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/interaction"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/calendar"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/weather"
	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const (
	cellGap    = 4
	chrome     = 4.6
	numberSize = 0.6
	barSize    = 0.5
	lineSize   = 0.52
)

type cellGeometry struct {
	width, height, number, bar, line float32
}

func (a *Agenda) month(w *gogui.Window, at, from time.Time, events []calendar.Event) gogui.View {
	_, weeks := monthGrid(at, a.first())
	reach := style.Reach()
	width := widgets.PageWidth(w)
	_, vh := w.WindowSize()
	g := cellGeometry{
		width:  (width - 6*cellGap) / 7,
		number: reach * numberSize,
		bar:    reach * barSize,
		line:   reach * lineSize,
	}
	g.height = max((float32(vh)-reach*chrome)/float32(weeks)-cellGap, g.number+2*g.line)
	rows := []gogui.View{a.weekdays(from, g.width)}
	today := day(time.Now())
	sky := a.forecast()
	for wk := range weeks {
		start := addDays(from, 7*wk)
		rows = append(rows, a.monthWeek(w, start, at.Month(), today, events, sky, g))
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingPx(cellGap),
		Clip:    true,
		Content: rows,
	})
}

func (a *Agenda) weekdays(from time.Time, width float32) gogui.View {
	small := widgets.Secondary()
	small.Align = gogui.TextAlignCenter
	var names []gogui.View
	for i := range 7 {
		names = append(names, gogui.Row(gogui.ContainerCfg{
			Width:   width,
			Sizing:  gogui.FixedFit,
			Padding: gogui.NoPadding,
			HAlign:  gogui.HAlignCenter,
			Content: []gogui.View{gogui.Label(addDays(from, i).Format("Mon"), small)},
		}))
	}
	return gogui.Row(gogui.ContainerCfg{
		Sizing:  gogui.FillFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingPx(cellGap),
		Content: names,
	})
}

func (a *Agenda) monthWeek(
	w *gogui.Window,
	start time.Time,
	month time.Month,
	today time.Time,
	events []calendar.Event,
	sky map[time.Time]weather.Day,
	g cellGeometry,
) gogui.View {
	placed := bars(events, start, 7)
	var content []gogui.View
	for i := range 7 {
		d := addDays(start, i)
		forecast, has := sky[d]
		cell := a.monthCell(w, d, d.Month() == month, d.Equal(today),
			lanes(placed, i), timed(events, d), g)
		if has {
			cell = gogui.Column(gogui.ContainerCfg{
				Sizing: gogui.FitFill, Padding: gogui.NoPadding,
				Content: []gogui.View{
					cell,
					gogui.Row(gogui.ContainerCfg{
						Float: true, FloatOffsetY: (g.number - g.number*0.55) / 2,
						Width: g.width - gogui.PadSmall, Sizing: gogui.FixedFit,
						Padding: gogui.NoPadding, HAlign: gogui.HAlignRight,
						Content: []gogui.View{a.weatherTag(forecast, g.number*0.55, false)},
					}),
				},
			})
		}
		content = append(content, cell)
	}
	for n, b := range placed {
		x := float32(b.from) * (g.width + cellGap)
		bw := float32(b.to-b.from+1)*(g.width+cellGap) - cellGap
		y := g.number + gogui.PadSmall/2 + float32(b.lane)*g.bar
		if y+g.bar > g.height {
			continue
		}
		content = append(content, gogui.Row(gogui.ContainerCfg{
			Float:        true,
			FloatOffsetX: x,
			FloatOffsetY: y,
			Padding:      gogui.NoPadding,
			Content: []gogui.View{
				a.barView(w, fmt.Sprintf("calendar-bar-%s-%d", start.Format("0102"), n),
					b, bw, g.bar-2),
			},
		}))
	}
	return gogui.Row(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingPx(cellGap),
		Clip:    true,
		Content: content,
	})
}

func (a *Agenda) barView(w *gogui.Window, id string, b bar, width, height float32) gogui.View {
	fill := a.color(b.event.Calendar)
	text := gogui.CurrentTheme().Cfg.TextStyleDef
	text.Size = height * 0.72
	text.Color = interaction.Contrast(fill)
	title := b.event.Summary
	if b.before {
		title = "‹ " + title
	}
	if b.after {
		title += " ›"
	}
	if !b.event.AllDay && !b.before {
		title = a.clock(b.event.Start) + " " + title
	}
	room := max(width-gogui.PadSmall*2, 1)
	e := b.event
	return a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
		ID: id, Width: width, Height: height, Sizing: gogui.FixedFixed,
		Padding: gogui.PadVH(0, gogui.PadSmall), VAlign: gogui.VAlignMiddle,
		Radius: gogui.RadiusSmall, Color: fill, Clip: true,
		Content: []gogui.View{gogui.Label(widgets.Ellipsize(w, title, text, room), text)},
	}, func(ev gogui.EventCtx) {
		a.detail(e)
		ev.Consume()
	})
}

func (a *Agenda) monthCell(
	w *gogui.Window,
	d time.Time,
	inMonth, today bool,
	stacked int,
	events []calendar.Event,
	g cellGeometry,
) gogui.View {
	t := gogui.CurrentTheme().Cfg
	number := t.TextStyleDef
	number.Size = g.number * 0.62
	if !inMonth {
		number.Color = widgets.Secondary().Color
	}
	mark := gogui.Label(strconv.Itoa(d.Day()), number)
	if today {
		number.Color = interaction.Contrast(t.ColorAccent)
		mark = gogui.Row(gogui.ContainerCfg{
			Height: g.number * 0.8, Sizing: gogui.FitFixed, VAlign: gogui.VAlignMiddle,
			Padding: gogui.PadVH(0, gogui.PadSmall),
			Radius:  gogui.RadiusPx(g.number * 0.4), Color: t.ColorAccent,
			Content: []gogui.View{gogui.Label(strconv.Itoa(d.Day()), number)},
		})
	}
	content := []gogui.View{gogui.Row(gogui.ContainerCfg{
		Height:  g.number,
		Sizing:  gogui.FillFixed,
		Padding: gogui.NoPadding,
		VAlign:  gogui.VAlignMiddle,
		Content: []gogui.View{mark},
	})}
	if stacked > 0 {
		content = append(content, gogui.Row(gogui.ContainerCfg{
			Height: float32(stacked) * g.bar, Sizing: gogui.FillFixed, Padding: gogui.NoPadding,
		}))
	}
	room := g.height - g.number - float32(stacked)*g.bar - gogui.PadSmall
	fits := max(int(room/g.line), 0)
	if len(events) > fits && fits > 0 {
		fits--
	}
	inner := g.width - gogui.PadSmall*2
	for i, e := range events {
		if i >= fits {
			more := widgets.Secondary()
			more.Size = g.line * 0.72
			content = append(
				content,
				gogui.Label(say.F("calendar.more", map[string]any{"N": len(events) - i}), more),
			)
			break
		}
		content = append(
			content,
			a.line(w, fmt.Sprintf("calendar-line-%s-%d", d.Format("0102"), i), e, inner, g.line),
		)
	}
	fill := t.ColorPanel
	if today {
		fill = t.ColorAccent
		fill.A = 0x30
	}
	at := d
	cfg := gogui.ContainerCfg{
		ID:      "calendar-cell-" + d.Format("20060102"),
		Width:   g.width,
		Sizing:  gogui.FixedFill,
		Padding: gogui.PadVH(0, gogui.PadSmall),
		Spacing: gogui.NoSpacing,
		Radius:  gogui.RadiusMedium,
		Color:   fill,
		Clip:    true,
		Content: content,
		OnGesture: func(e gogui.EventCtx) {
			if e.Event != nil && e.Event.GestureType == gogui.GestureLongPress &&
				e.Event.GesturePhase == gogui.GesturePhaseBegan && len(a.o.Calendar.Writable()) > 0 {
				a.add(time.Date(at.Year(), at.Month(), at.Day(), 9, 0, 0, 0, at.Location()))
				e.Consume()
			}
		},
	}
	return a.o.UI.Toolkit.Pressable(gogui.Column, cfg, func(e gogui.EventCtx) {
		a.show(schema.CalendarDay, at)
		e.Consume()
	})
}

func (a *Agenda) line(
	w *gogui.Window,
	id string,
	e calendar.Event,
	width, height float32,
) gogui.View {
	when := gogui.CurrentTheme().Cfg.TextStyleDef
	when.Size = height * 0.72
	when.Color = a.color(e.Calendar)
	what := gogui.CurrentTheme().Cfg.TextStyleDef
	what.Size = height * 0.72
	stamp := a.clock(e.Start)
	room := max(width-w.TextWidth(stamp, when)-gogui.PadSmall, 1)
	ev := e
	return a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
		ID:      id,
		Width:   width,
		Height:  height,
		Sizing:  gogui.FixedFixed,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingPx(gogui.PadSmall / 2),
		VAlign:  gogui.VAlignMiddle,
		Clip:    true,
		Content: []gogui.View{
			gogui.Label(stamp, when),
			gogui.Label(widgets.Ellipsize(w, e.Summary, what, room), what),
		},
	}, func(c gogui.EventCtx) {
		a.detail(ev)
		c.Consume()
	})
}

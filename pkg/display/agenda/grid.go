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
	gridID       = "calendar-hours"
	hourSize     = 1.0
	gutterSize   = 1.1
	narrowColumn = 1.8
	shortBlock   = 0.45
	morning      = 7
)

func (a *Agenda) grid(w *gogui.Window, days []time.Time, events []calendar.Event) gogui.View {
	reach := style.Reach()
	width := widgets.PageWidth(w)
	gutter := reach * gutterSize
	colW := (width - gutter - float32(len(days)-1)*cellGap) / float32(len(days))
	if len(days) > 1 && colW < reach*narrowColumn {
		return a.lists(w, days, events)
	}
	hourH := reach * hourSize
	a.scrollTo(w, days, events, hourH)
	content := []gogui.View{}
	sky := a.forecast()
	if len(days) > 1 || len(sky) > 0 {
		content = append(content, a.dayHeads(days, sky, gutter, colW))
	}
	if strip := a.allDay(w, days, events, gutter, colW); strip != nil {
		content = append(content, strip)
	}
	cols := []gogui.View{a.gutter(gutter, hourH)}
	now := time.Now()
	for _, d := range days {
		cols = append(cols, a.dayColumn(w, d, timed(events, d), colW, hourH, now))
	}
	content = append(content, gogui.Column(gogui.ContainerCfg{
		ID:         gridID,
		Sizing:     gogui.FillFill,
		Padding:    gogui.NoPadding,
		Scrollable: true,
		ScrollMode: gogui.ScrollVerticalOnly,
		Clip:       true,
		Content: []gogui.View{gogui.Row(gogui.ContainerCfg{
			Sizing:  gogui.FillFit,
			Padding: gogui.NoPadding,
			Spacing: gogui.SpacingPx(cellGap),
			Content: cols,
		})},
	}))
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingSmall,
		Content: content,
	})
}

func (a *Agenda) scrollTo(
	w *gogui.Window,
	days []time.Time,
	events []calendar.Event,
	hourH float32,
) {
	a.mu.Lock()
	due := a.scroll
	a.scroll = false
	a.mu.Unlock()
	if !due {
		return
	}
	hour := morning
	now := time.Now()
	for _, d := range days {
		for _, e := range timed(events, d) {
			hour = min(hour, e.Start.Hour())
		}
	}
	for _, d := range days {
		if d.Equal(day(now)) {
			hour = now.Hour()
		}
	}
	offset := float32(max(hour-1, 0)) * hourH
	w.QueueCommand(func(w *gogui.Window) {
		for _, id := range w.ResolveID(gridID) {
			w.ScrollVerticalTo(id, -offset)
		}
	})
}

func (a *Agenda) dayHeads(
	days []time.Time,
	sky map[time.Time]weather.Day,
	gutter, colW float32,
) gogui.View {
	t := gogui.CurrentTheme().Cfg
	today := day(time.Now())
	heads := []gogui.View{spacer(gutter, 1)}
	for _, d := range days {
		name, number := widgets.Secondary(), t.TextStyleDef
		if d.Equal(today) {
			number.Color = t.ColorAccent
		}
		content := []gogui.View{
			gogui.Label(d.Format("Mon"), name),
			gogui.Label(strconv.Itoa(d.Day()), number),
		}
		if f, ok := sky[d]; ok {
			content = append(content, a.weatherTag(f, style.Reach()*0.4, true))
		}
		at := d
		heads = append(heads, a.o.UI.Toolkit.Pressable(gogui.Column, gogui.ContainerCfg{
			ID: "calendar-head-" + d.Format("20060102"), Width: colW, Sizing: gogui.FixedFit,
			Padding: gogui.NoPadding, HAlign: gogui.HAlignCenter, Radius: gogui.RadiusMedium,
			Content: content,
		}, func(e gogui.EventCtx) {
			a.show(schema.CalendarDay, at)
			e.Consume()
		}))
	}
	return gogui.Row(gogui.ContainerCfg{
		Sizing:  gogui.FillFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingPx(cellGap),
		Content: heads,
	})
}

func (a *Agenda) allDay(
	w *gogui.Window,
	days []time.Time,
	events []calendar.Event,
	gutter, colW float32,
) gogui.View {
	placed := bars(events, days[0], len(days))
	if len(placed) == 0 {
		return nil
	}
	barH := style.Reach() * barSize
	rows := 0
	for _, b := range placed {
		rows = max(rows, b.lane+1)
	}
	content := []gogui.View{}
	for n, b := range placed {
		x := gutter + cellGap + float32(b.from)*(colW+cellGap)
		bw := float32(b.to-b.from+1)*(colW+cellGap) - cellGap
		content = append(content, gogui.Row(gogui.ContainerCfg{
			Float:        true,
			FloatOffsetX: x,
			FloatOffsetY: float32(b.lane) * barH,
			Padding:      gogui.NoPadding,
			Content: []gogui.View{
				a.barView(w, fmt.Sprintf("calendar-strip-%d", n), b, bw, barH-2),
			},
		}))
	}
	return gogui.Row(gogui.ContainerCfg{
		Height: float32(
			rows,
		) * barH,
		Sizing:  gogui.FillFixed,
		Padding: gogui.NoPadding,
		Content: content,
	})
}

func (a *Agenda) gutter(width, hourH float32) gogui.View {
	small := widgets.Secondary()
	var hours []gogui.View
	for h := range 24 {
		label := ""
		if h > 0 {
			label = a.clock(time.Date(2000, 1, 1, h, 0, 0, 0, time.Local))
		}
		hours = append(hours, gogui.Row(gogui.ContainerCfg{
			Width: width, Height: hourH, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
			HAlign: gogui.HAlignRight, VAlign: gogui.VAlignTop,
			Content: []gogui.View{gogui.Label(label, small)},
		}))
	}
	return gogui.Column(gogui.ContainerCfg{
		Width:   width,
		Sizing:  gogui.FixedFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.NoSpacing,
		Content: hours,
	})
}

func (a *Agenda) dayColumn(
	w *gogui.Window,
	d time.Time,
	events []calendar.Event,
	width, hourH float32,
	now time.Time,
) gogui.View {
	t := gogui.CurrentTheme().Cfg
	line, fill := t.ColorBackground, t.ColorPanel
	today := d.Equal(day(now))
	if today {
		fill = t.ColorAccent
		fill.A = 0x30
	}
	mark := widgets.Color(a.o.UI.Palette().Danger)
	radius := float32(gogui.PadSmall)
	shapes := a.shapes(w, d, events, width, hourH)
	at := d
	return gogui.DrawCanvas(gogui.DrawCanvasCfg{
		ID:      "calendar-day-" + d.Format("20060102"),
		Width:   width,
		Height:  24 * hourH,
		Sizing:  gogui.FixedFixed,
		Version: uint64(now.UnixNano()),
		OnDraw: func(dc *gogui.DrawContext) {
			dc.FilledRoundedRect(0, 0, width, 24*hourH, radius*2, fill)
			for h := 1; h < 24; h++ {
				dc.FilledRect(0, float32(h)*hourH, width, 1, line)
			}
			for _, s := range shapes {
				dc.FilledRoundedRect(s.x, s.y, s.w, s.h, radius, s.fill)
				for i, text := range s.lines {
					dc.Text(
						s.x+gogui.PadSmall,
						s.y+gogui.PadSmall/2+float32(i)*s.text.Size*1.25,
						text,
						s.text,
					)
				}
			}
			if today {
				dc.FilledRect(0, minutes(now)*hourH/60, width, 2, mark)
			}
		},
		OnClick: func(e gogui.EventCtx) {
			if e.Event == nil {
				return
			}
			x, y := e.Event.MouseX-e.Layout.Shape.X, e.Event.MouseY-e.Layout.Shape.Y
			for i := len(shapes) - 1; i >= 0; i-- {
				if s := shapes[i]; x >= s.x && x <= s.x+s.w && y >= s.y && y <= s.y+s.h {
					a.detail(s.event)
					e.Consume()
					return
				}
			}
		},
		OnGesture: func(e gogui.EventCtx) {
			if e.Event == nil || e.Event.GestureType != gogui.GestureLongPress ||
				e.Event.GesturePhase != gogui.GesturePhaseBegan || len(a.o.Calendar.Writable()) == 0 {
				return
			}
			hour := min(max(int((e.Event.MouseY-e.Layout.Shape.Y)/hourH), 0), 23)
			a.add(time.Date(at.Year(), at.Month(), at.Day(), hour, 0, 0, 0, at.Location()))
			e.Consume()
		},
	})
}

func minutes(t time.Time) float32 { return float32(t.Hour()*60 + t.Minute()) }

type shape struct {
	event      calendar.Event
	x, y, w, h float32
	fill       gogui.Color
	text       gogui.TextStyle
	lines      []string
}

func (a *Agenda) shapes(
	w *gogui.Window,
	d time.Time,
	events []calendar.Event,
	width, hourH float32,
) []shape {
	var out []shape
	for _, b := range columns(events) {
		e := b.event
		end := e.End
		if !end.After(e.Start) {
			end = e.Start.Add(30 * time.Minute)
		}
		top := minutes(e.Start) * hourH / 60
		bottom := 24 * hourH
		if day(end).Equal(d) {
			bottom = minutes(end) * hourH / 60
		}
		height := max(bottom-top, style.Reach()*shortBlock) - 2
		slot := width / float32(b.cols)
		fill := a.color(e.Calendar)
		text := gogui.CurrentTheme().Cfg.TextStyleDef
		text.Size = min(text.Size, style.Reach()*0.3)
		text.Color = interaction.Contrast(fill)
		room := max(slot-2-gogui.PadSmall*2, 1)
		lines := []string{widgets.Ellipsize(w, e.Summary, text, room)}
		if height > text.Size*2.6 {
			lines = append(
				lines,
				widgets.Ellipsize(w, a.clock(e.Start)+" – "+a.clock(end), text, room),
			)
		}
		out = append(out, shape{
			event: e,
			x:     float32(b.col) * slot, y: top, w: slot - 2, h: height,
			fill: fill, text: text, lines: lines,
		})
	}
	return out
}

func (a *Agenda) lists(w *gogui.Window, days []time.Time, events []calendar.Event) gogui.View {
	t := gogui.CurrentTheme().Cfg
	width := widgets.PageWidth(w)
	today := day(time.Now())
	var rows []gogui.View
	for _, d := range days {
		head := t.TextStyleDef
		if d.Equal(today) {
			head.Color = t.ColorAccent
		}
		at := d
		rows = append(rows, a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
			ID: "calendar-list-" + d.Format(
				"20060102",
			),
			Sizing:  gogui.FillFit,
			Padding: gogui.NoPadding,
			Content: []gogui.View{gogui.Label(d.Format("Monday, January 2"), head)},
		}, func(e gogui.EventCtx) {
			a.show(schema.CalendarDay, at)
			e.Consume()
		}))
		end := addDays(d, 1)
		shown := 0
		for i, e := range events {
			if !e.Overlaps(d, end) {
				continue
			}
			shown++
			rows = append(
				rows,
				a.listRow(w, fmt.Sprintf("calendar-item-%s-%d", d.Format("0102"), i), e, width),
			)
		}
		if shown == 0 {
			rows = append(rows, gogui.Label(say.T("calendar.free"), widgets.Secondary()))
		}
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		Scrollable: true, ScrollMode: gogui.ScrollVerticalOnly, Clip: true, Content: rows,
	})
}

func (a *Agenda) listRow(w *gogui.Window, id string, e calendar.Event, width float32) gogui.View {
	t := gogui.CurrentTheme().Cfg
	side := style.Reach() * 0.2
	when := say.T("calendar.allday")
	if !spans(e) {
		when = a.clock(e.Start)
	}
	small := widgets.Secondary()
	stamp := w.TextWidth(when, small)
	room := max(width-side-stamp-gogui.PadSmall*4, 1)
	ev := e
	return a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
		ID: id, Sizing: gogui.FillFit, Padding: gogui.PaddingSmall, Spacing: gogui.SpacingSmall,
		VAlign: gogui.VAlignMiddle, Radius: gogui.RadiusMedium, Color: t.ColorPanel,
		Content: []gogui.View{
			gogui.Row(gogui.ContainerCfg{
				Width: side, Height: side, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
				Radius: gogui.RadiusPx(side / 2), Color: a.color(e.Calendar),
			}),
			gogui.Label(when, small),
			gogui.Label(widgets.Ellipsize(w, e.Summary, t.TextStyleDef, room), t.TextStyleDef),
		},
	}, func(c gogui.EventCtx) {
		a.detail(ev)
		c.Consume()
	})
}

func spacer(width, height float32) gogui.View {
	return gogui.Row(gogui.ContainerCfg{
		Width: width, Height: height, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
	})
}

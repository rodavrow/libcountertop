package agenda

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/display/menu"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/calendar"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/say"
)

const (
	saveWait   = 15 * time.Second
	pickDays   = 90
	longestDay = 14
)

var lengths = []time.Duration{
	15 * time.Minute, 30 * time.Minute, 45 * time.Minute, time.Hour,
	90 * time.Minute, 2 * time.Hour, 3 * time.Hour, 4 * time.Hour,
	6 * time.Hour, 8 * time.Hour, 12 * time.Hour,
}

func (a *Agenda) when(e calendar.Event) string {
	first, last := day(e.Start), lastDay(e)
	if e.AllDay {
		if first.Equal(last) {
			return first.Format("Monday, January 2")
		}
		return first.Format("Mon, Jan 2") + " – " + last.Format("Mon, Jan 2")
	}
	if first.Equal(last) {
		return e.Start.Format(
			"Monday, January 2",
		) + " · " + a.clock(
			e.Start,
		) + " – " + a.clock(
			e.End,
		)
	}
	return e.Start.Format(
		"Mon, Jan 2",
	) + " " + a.clock(
		e.Start,
	) + " – " + e.End.Format(
		"Mon, Jan 2",
	) + " " + a.clock(
		e.End,
	)
}

func (a *Agenda) detail(e calendar.Event) {
	pal := a.o.UI.Palette()
	a.o.Shell.Push(&shell.Page{
		Title: e.Summary,
		Build: func() ([]menu.Row, []func(int)) {
			rows := []menu.Row{
				{Glyph: "mdi:clock-outline", Label: a.when(e)},
				{
					Glyph:   "mdi:calendar",
					Label:   a.o.Calendar.Title(e.Calendar),
					Preview: widgets.Dot(pal.Roles.Or(a.o.Calendar.Color(e.Calendar))),
				},
			}
			if l := strings.TrimSpace(e.Location); l != "" {
				rows = append(rows, menu.Row{Glyph: "mdi:map-marker-outline", Label: l})
			}
			if d := strings.TrimSpace(e.Description); d != "" {
				rows = append(rows, menu.Row{Glyph: "mdi:text", Label: d})
			}
			return rows, make([]func(int), len(rows))
		},
	})
}

type draft struct {
	calendar.Draft
	length  time.Duration
	days    int
	problem string
	saving  bool
}

func (a *Agenda) add(start time.Time) {
	writable := a.o.Calendar.Writable()
	if len(writable) == 0 {
		return
	}
	d := &draft{
		Draft:  calendar.Draft{Calendar: writable[0], Start: start},
		length: time.Hour,
		days:   1,
	}
	page := &shell.Page{Title: say.T("calendar.add")}
	page.Build = func() ([]menu.Row, []func(int)) { return a.form(d) }
	page.Actions = []shell.Action{{
		Label: say.T("calendar.save"),
		Glyph: "mdi:content-save-outline",
		Do:    func() { a.save(d) },
	}}
	a.o.Shell.Push(page)
}

func (a *Agenda) form(d *draft) ([]menu.Row, []func(int)) {
	pal := a.o.UI.Palette()
	set := func(edit func()) {
		edit()
		a.o.Shell.Redraw()
	}
	var rows []menu.Row
	var taps []func(int)
	a.mu.Lock()
	problem := d.problem
	a.mu.Unlock()
	if problem != "" {
		rows = append(rows, menu.Row{Glyph: "mdi:alert-circle-outline", Label: problem})
		taps = append(taps, nil)
	}
	rows = append(
		rows,
		menu.Row{
			Label: say.T(
				"calendar.title",
			),
			Kind:  menu.Field,
			Value: d.Summary,
			Focus: d.Summary == "",
			Save:  func(s string) { set(func() { d.Summary = strings.TrimSpace(s) }) },
		},
		menu.Row{
			Label:   say.T("calendar.setting.entities"),
			Kind:    menu.Chevron,
			Value:   a.o.Calendar.Title(d.Calendar),
			Preview: widgets.Dot(pal.Roles.Or(a.o.Calendar.Color(d.Calendar))),
		},
		menu.Row{Label: say.T("calendar.allday"), Kind: menu.Toggle, On: d.AllDay},
		menu.Row{
			Label: say.T("calendar.date"),
			Kind:  menu.Chevron,
			Value: d.Start.Format("Mon, Jan 2"),
		},
	)
	taps = append(taps,
		nil,
		func(int) { a.o.Shell.Push(a.pickCalendar(d)) },
		func(int) { set(func() { d.AllDay = !d.AllDay }) },
		func(int) { a.o.Shell.Push(a.pickDate(d)) },
	)
	if d.AllDay {
		steps := menu.Steps(longestDay)
		rows = append(rows, menu.Row{
			Label: say.T("calendar.days"), Kind: menu.Slider,
			Level: steps.Level(d.days - 1), Snap: steps.Snap,
			Value: say.F("calendar.count", map[string]any{"N": d.days}),
		})
		taps = append(taps, func(level int) { set(func() { d.days = steps.Index(level) + 1 }) })
	} else {
		hours, quarters, spans := menu.Steps(24), menu.Steps(4), menu.Steps(len(lengths))
		rows = append(rows,
			menu.Row{
				Label: say.T("calendar.hour"), Kind: menu.Slider,
				Level: hours.Level(d.Start.Hour()), Snap: hours.Snap, Value: a.clock(d.Start),
			},
			menu.Row{
				Label: say.T("calendar.minute"),
				Kind:  menu.Slider,
				Level: quarters.Level(
					d.Start.Minute() / 15,
				),
				Snap:  quarters.Snap,
				Value: d.Start.Format(":04"),
			},
			menu.Row{
				Label: say.T("calendar.length"), Kind: menu.Slider,
				Level: spans.Level(max(slices.Index(lengths, d.length), 0)), Snap: spans.Snap,
				Value: length(d.length),
			},
		)
		taps = append(
			taps,
			func(level int) { set(func() { d.Start = clockAt(d.Start, hours.Index(level), d.Start.Minute()) }) },
			func(level int) {
				set(func() { d.Start = clockAt(d.Start, d.Start.Hour(), 15*quarters.Index(level)) })
			},
			func(level int) { set(func() { d.length = lengths[spans.Index(level)] }) },
		)
	}
	rows = append(rows,
		menu.Row{
			Label: say.T("calendar.location"), Kind: menu.Field, Value: d.Location,
			Save: func(s string) { set(func() { d.Location = s }) },
		},
		menu.Row{
			Label: say.T("calendar.notes"), Kind: menu.Field, Value: d.Description,
			Save: func(s string) { set(func() { d.Description = s }) },
		},
	)
	taps = append(taps, nil, nil)
	return rows, taps
}

func clockAt(d time.Time, hour, minute int) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, d.Location())
}

func length(d time.Duration) string {
	if d < time.Hour {
		return say.F("calendar.minutes", map[string]any{"N": int(d / time.Minute)})
	}
	if d%time.Hour == 0 {
		return say.F("calendar.hours", map[string]any{"N": int(d / time.Hour)})
	}
	return say.F("calendar.hours.half", map[string]any{"N": int(d / time.Hour)})
}

func (a *Agenda) pickCalendar(d *draft) *shell.Page {
	pal := a.o.UI.Palette()
	return &shell.Page{
		Title: say.T("calendar.setting.entities"),
		Build: func() ([]menu.Row, []func(int)) {
			var rows []menu.Row
			var taps []func(int)
			for _, e := range a.o.Calendar.Writable() {
				rows = append(rows, menu.Row{
					Label:   a.o.Calendar.Title(e),
					Preview: widgets.Dot(pal.Roles.Or(a.o.Calendar.Color(e))),
					Chosen:  e == d.Calendar,
				})
				taps = append(taps, func(int) {
					d.Calendar = e
					a.o.Shell.Pop()
				})
			}
			return rows, taps
		},
	}
}

func (a *Agenda) pickDate(d *draft) *shell.Page {
	return &shell.Page{Title: say.T("calendar.date"), Build: func() ([]menu.Row, []func(int)) {
		today := day(time.Now())
		first := today
		if day(d.Start).Before(today) {
			first = day(d.Start)
		}
		var rows []menu.Row
		var taps []func(int)
		for i := range pickDays {
			date := addDays(first, i)
			label := date.Format("Monday, January 2")
			switch {
			case date.Equal(today):
				label = say.T("calendar.today")
			case date.Equal(addDays(today, 1)):
				label = say.T("calendar.tomorrow")
			}
			rows = append(rows, menu.Row{Label: label, Chosen: date.Equal(day(d.Start))})
			taps = append(taps, func(int) {
				d.Start = time.Date(
					date.Year(),
					date.Month(),
					date.Day(),
					d.Start.Hour(),
					d.Start.Minute(),
					0,
					0,
					d.Start.Location(),
				)
				a.o.Shell.Pop()
			})
		}
		return rows, taps
	}}
}

func (a *Agenda) save(d *draft) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d.saving {
		return
	}
	if d.Summary == "" {
		d.problem = say.T("calendar.needs.title")
		a.o.Shell.Redraw()
		return
	}
	out := d.Draft
	if d.AllDay {
		out.Start = day(d.Start)
		out.End = addDays(out.Start, d.days)
	} else {
		out.End = d.Start.Add(d.length)
	}
	d.saving, d.problem = true, ""
	safe.Go("calendar add", func() {
		ctx, cancel := context.WithTimeout(context.Background(), saveWait)
		defer cancel()
		err := a.o.Calendar.Create(ctx, out)
		a.mu.Lock()
		d.saving = false
		if err != nil {
			d.problem = say.T("calendar.failed")
		}
		a.mu.Unlock()
		if err != nil {
			a.o.Shell.Redraw()
			return
		}
		a.o.Shell.Pop()
		v, _ := a.state()
		a.show(v, out.Start)
	})
}

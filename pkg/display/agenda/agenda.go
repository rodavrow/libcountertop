package agenda

import (
	"strconv"
	"sync"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/apps"
	"github.com/ygelfand/libcountertop/pkg/display/icons"
	"github.com/ygelfand/libcountertop/pkg/display/interaction"
	"github.com/ygelfand/libcountertop/pkg/display/menu"
	"github.com/ygelfand/libcountertop/pkg/display/navigation"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/calendar"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/weather"
	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const (
	page      = "agenda"
	headSize  = 1.5
	labelSize = 0.42
)

type View struct{}

func (*View) Covers() bool { return true }

func (*View) Timeout() time.Duration { return shell.AppTimeout }

type Forecast interface {
	Daily() []weather.Day
}

type Options struct {
	UI         *widgets.UI
	Shell      *shell.Shell
	Calendar   *calendar.Calendar
	Forecast   Forecast
	Gate       func(*gogui.Window) gogui.View
	TwentyFour func() bool
	Settings   func() *shell.Page
}

type Agenda struct {
	o    Options
	view *View

	mu     sync.Mutex
	shown  schema.CalendarView
	anchor time.Time
	hidden map[string]bool
	scroll bool
}

func New(o Options) *Agenda { return &Agenda{o: o, view: &View{}, hidden: map[string]bool{}} }

func (a *Agenda) App() apps.App {
	return apps.App{
		ID:   schema.AppCalendar,
		Name: func() string { return say.T("calendar.app") },
		Icon: "mdi:calendar",
		Open: a.Open,
	}
}

func (a *Agenda) Open() {
	a.o.Calendar.Fetch()
	a.mu.Lock()
	a.shown, a.anchor, a.hidden, a.scroll = a.o.Calendar.Read().
		Opens(),
		day(
			time.Now(),
		), map[string]bool{}, true
	a.mu.Unlock()
	a.o.Shell.Push(a.view)
}

func (a *Agenda) Screen(v shell.View) *navigation.Screen {
	if v != a.view {
		return nil
	}
	return &navigation.Screen{
		Title:    say.T("calendar.app"),
		Head:     a.head,
		View:     v,
		Build:    a.build,
		Fixed:    true,
		Settings: a.settings(),
	}
}

func (a *Agenda) settings() func() {
	if a.o.Settings == nil {
		return nil
	}
	return func() { a.o.Shell.OpenSettings(a.o.Settings()) }
}

func (a *Agenda) state() (schema.CalendarView, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.shown, a.anchor
}

func (a *Agenda) show(v schema.CalendarView, at time.Time) {
	a.mu.Lock()
	a.shown, a.anchor, a.scroll = v, day(at), true
	a.mu.Unlock()
	a.o.Calendar.SetView(v)
	a.o.Shell.Redraw()
}

func (a *Agenda) page(step int) {
	v, at := a.state()
	switch v {
	case schema.CalendarDay:
		at = addDays(at, step)
	case schema.CalendarWeek:
		at = addDays(at, 7*step)
	default:
		at = time.Date(at.Year(), at.Month()+time.Month(step), 1, 0, 0, 0, 0, at.Location())
	}
	a.show(v, at)
}

func (a *Agenda) first() time.Weekday {
	if say.T("calendar.week.start") == "monday" {
		return time.Monday
	}
	return time.Sunday
}

func (a *Agenda) span(v schema.CalendarView, at time.Time) (time.Time, time.Time) {
	switch v {
	case schema.CalendarDay:
		return at, addDays(at, 1)
	case schema.CalendarWeek:
		from := weekOf(at, a.first())
		return from, addDays(from, 7)
	}
	from, weeks := monthGrid(at, a.first())
	return from, addDays(from, 7*weeks)
}

func (a *Agenda) visible(events []calendar.Event) []calendar.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := events[:0:0]
	for _, e := range events {
		if !a.hidden[e.Calendar] {
			out = append(out, e)
		}
	}
	return out
}

func (a *Agenda) toggle(entity string) {
	a.mu.Lock()
	a.hidden[entity] = !a.hidden[entity]
	a.mu.Unlock()
	a.o.Shell.Redraw()
}

func (a *Agenda) build(w *gogui.Window) gogui.View {
	if a.o.Gate != nil {
		if gate := a.o.Gate(w); gate != nil {
			return gate
		}
	}
	if len(a.o.Calendar.Chosen()) == 0 {
		var pick func(int)
		if open := a.settings(); open != nil {
			pick = func(int) { open() }
		}
		return a.o.UI.Row("none", page, menu.Row{Label: say.T("calendar.pick")}, pick, false)
	}
	v, at := a.state()
	from, to := a.span(v, at)
	events, _, _ := a.o.Calendar.Events(from, to)
	events = a.visible(events)
	var body gogui.View
	switch v {
	case schema.CalendarDay:
		body = a.grid(w, []time.Time{at}, events)
	case schema.CalendarWeek:
		days := make([]time.Time, 7)
		for i := range days {
			days[i] = addDays(from, i)
		}
		body = a.grid(w, days, events)
	default:
		body = a.month(w, at, from, events)
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingSmall,
		Content: []gogui.View{a.legend(), a.swiper(body)},
	})
}

func (a *Agenda) swiper(body gogui.View) gogui.View {
	return gogui.Column(gogui.ContainerCfg{
		ID:      "calendar-body",
		Sizing:  gogui.FillFill,
		Padding: gogui.NoPadding,
		Content: []gogui.View{body},
		OnGesture: func(e gogui.EventCtx) {
			ev := e.Event
			if ev == nil || ev.GestureType != gogui.GestureSwipe ||
				ev.VelocityX*ev.VelocityX <= ev.VelocityY*ev.VelocityY {
				return
			}
			if ev.VelocityX < 0 {
				a.page(1)
			} else {
				a.page(-1)
			}
			e.Consume()
		},
	})
}

func (a *Agenda) head(*gogui.Window) gogui.View {
	if len(a.o.Calendar.Chosen()) == 0 {
		return gogui.Label(say.T("calendar.app"), gogui.CurrentTheme().TextStyleDisplay)
	}
	v, at := a.state()
	from, to := a.span(v, at)
	_, err, loaded := a.o.Calendar.Events(from, to)
	status := ""
	switch {
	case err != nil:
		status = say.T("home.failed")
	case !loaded:
		status = say.T("home.loading")
	}
	items := []gogui.View{
		a.button("calendar-back", "mdi:chevron-left", "", func() { a.page(-1) }),
		a.button("calendar-next", "mdi:chevron-right", "", func() { a.page(1) }),
	}
	if !a.showsToday(v, at) {
		items = append(
			items,
			a.button(
				"calendar-today",
				"",
				say.T("calendar.today"),
				func() { a.show(v, time.Now()) },
			),
		)
	}
	items = append(items, gogui.Label(a.heading(v, at), gogui.CurrentTheme().TextStyleDisplay))
	if status != "" {
		items = append(items, gogui.Label(status, widgets.Secondary()))
	}
	items = append(
		items,
		gogui.Row(gogui.ContainerCfg{Sizing: gogui.FillFit, Padding: gogui.NoPadding}),
	)
	for _, choice := range schema.CalendarViews() {
		items = append(items, a.pill("calendar-view-"+string(choice), choice.Label(), choice == v,
			func() { a.show(choice, at) }))
	}
	if len(a.o.Calendar.Writable()) > 0 {
		items = append(
			items,
			a.button("calendar-add", "mdi:plus", "", func() { a.add(a.suggest(v, at)) }),
		)
	}
	return gogui.Row(gogui.ContainerCfg{
		Sizing:  gogui.FillFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingSmall,
		VAlign:  gogui.VAlignMiddle,
		Content: items,
	})
}

func (a *Agenda) showsToday(v schema.CalendarView, at time.Time) bool {
	today := day(time.Now())
	switch v {
	case schema.CalendarDay:
		return at.Equal(today)
	case schema.CalendarWeek:
		return weekOf(at, a.first()).Equal(weekOf(today, a.first()))
	}
	return at.Year() == today.Year() && at.Month() == today.Month()
}

func (a *Agenda) heading(v schema.CalendarView, at time.Time) string {
	switch v {
	case schema.CalendarDay:
		return at.Format("Monday, January 2")
	case schema.CalendarWeek:
		from := weekOf(at, a.first())
		to := addDays(from, 6)
		if from.Month() == to.Month() {
			return from.Format("January 2") + " – " + strconv.Itoa(to.Day())
		}
		return from.Format("Jan 2") + " – " + to.Format("Jan 2")
	}
	return at.Format("January 2006")
}

func (a *Agenda) suggest(v schema.CalendarView, at time.Time) time.Time {
	now := time.Now()
	start := time.Date(at.Year(), at.Month(), at.Day(), now.Hour()+1, 0, 0, 0, at.Location())
	if v == schema.CalendarMonth && !day(now).Equal(at) {
		start = time.Date(at.Year(), at.Month(), at.Day(), 9, 0, 0, 0, at.Location())
	}
	return start
}

func (a *Agenda) legend() gogui.View {
	pal := a.o.UI.Palette()
	a.mu.Lock()
	hidden := a.hidden
	a.mu.Unlock()
	var chips []gogui.View
	for _, e := range a.o.Calendar.Chosen() {
		color := widgets.Color(pal.Roles.Or(a.o.Calendar.Color(e)))
		text := gogui.CurrentTheme().Cfg.TextStyleDef
		fill := color
		fill.A = 0x40
		if hidden[e] {
			text = widgets.Secondary()
			fill = gogui.Color{}
		}
		side := style.Reach() * 0.22
		chips = append(chips, a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
			ID:      "calendar-chip-" + e,
			Sizing:  gogui.FitFit,
			Padding: gogui.PaddingSmall,
			Spacing: gogui.SpacingSmall,
			VAlign:  gogui.VAlignMiddle,
			Radius:  gogui.RadiusLarge,
			Color:   fill,
			Content: []gogui.View{
				gogui.Row(gogui.ContainerCfg{
					Width: side, Height: side, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
					Radius: gogui.RadiusPx(side / 2), Color: color,
				}),
				gogui.Label(a.o.Calendar.Title(e), text),
			},
		}, func(ev gogui.EventCtx) {
			a.toggle(e)
			ev.Consume()
		}))
	}
	return gogui.Row(gogui.ContainerCfg{
		Sizing:  gogui.FillFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingSmall,
		Content: chips,
	})
}

func headSide() float32 { return style.Reach() * headSize }

func headText() gogui.TextStyle {
	st := gogui.CurrentTheme().Cfg.TextStyleDef
	st.Size = style.Reach() * labelSize
	return st
}

func (a *Agenda) button(id, glyph, label string, do func()) gogui.View {
	t := gogui.CurrentTheme().Cfg
	side := headSide()
	cfg := gogui.ContainerCfg{
		ID:      id,
		Height:  side,
		Sizing:  gogui.FitFixed,
		VAlign:  gogui.VAlignMiddle,
		HAlign:  gogui.HAlignCenter,
		Padding: gogui.PadVH(0, gogui.PadMedium),
		Radius:  gogui.RadiusLarge,
		Color:   t.ColorPanel,
	}
	if glyph != "" {
		cfg.Width, cfg.Sizing, cfg.Padding = side, gogui.FixedFixed, gogui.NoPadding
		cfg.Content = []gogui.View{icon(glyph, t.TextStyleDef.Color, side*0.5)}
	}
	if label != "" {
		cfg.Content = append(cfg.Content, gogui.Label(label, headText()))
	}
	return a.o.UI.Toolkit.Pressable(gogui.Row, cfg, func(e gogui.EventCtx) {
		do()
		e.Consume()
	})
}

func (a *Agenda) pill(id, label string, chosen bool, do func()) gogui.View {
	t := gogui.CurrentTheme().Cfg
	text, fill := headText(), t.ColorPanel
	if chosen {
		fill = t.ColorAccent
		text.Color = interaction.Contrast(fill)
	}
	return a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
		ID: id, Height: headSide(), Sizing: gogui.FitFixed, VAlign: gogui.VAlignMiddle,
		Padding: gogui.PadVH(0, gogui.PadMedium), Radius: gogui.RadiusLarge, Color: fill,
		Content: []gogui.View{gogui.Label(label, text)},
	}, func(e gogui.EventCtx) {
		do()
		e.Consume()
	})
}

func (a *Agenda) clock(t time.Time) string {
	if a.o.TwentyFour != nil && a.o.TwentyFour() {
		return t.Format("15:04")
	}
	if t.Minute() == 0 {
		return t.Format("3pm")
	}
	return t.Format("3:04pm")
}

func icon(name string, color gogui.Color, size float32) gogui.View {
	st := widgets.IconStyle(color)
	st.Family, st.Size = icons.Family, size
	return gogui.Label(icons.Glyph(icons.Named(name)), st)
}

func (a *Agenda) color(entity string) gogui.Color {
	return widgets.Color(a.o.UI.Palette().Roles.Or(a.o.Calendar.Color(entity)))
}

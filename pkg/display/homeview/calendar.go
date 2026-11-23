package homeview

import (
	"fmt"
	"slices"
	"sync"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/menu"
	"github.com/ygelfand/libcountertop/pkg/display/settings"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/theme"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/calendar"
	"github.com/ygelfand/libcountertop/pkg/say"
	schema "github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const colorSettle = 400 * time.Millisecond

func (view *Renderer) CalendarPage() *shell.Page {
	c := view.options.Calendar
	c.Fetch()
	e := &colorEditor{view: view, cal: c}
	return &shell.Page{
		Title: say.T("calendar.app"),
		Side:  e.side,
		Build: func() ([]menu.Row, []func(int)) {
			cfg := c.Read()
			rows := []menu.Row{
				settings.ModeRow(
					settings.ModeMarks{},
					say.T("calendar.setting.opening"),
					schema.CalendarOpenings(),
					cfg.Opening,
				),
				{Label: say.T("calendar.setting.weather"), Kind: menu.Toggle, On: cfg.Weather},
				{Label: say.T("calendar.setting.saver"), Kind: menu.Toggle, On: cfg.Saver},
			}
			taps := []func(int){
				settings.ModePick(schema.CalendarOpenings(), c.SetOpening),
				func(int) { c.SetWeather(!cfg.Weather) },
				func(int) { c.SetSaver(!cfg.Saver) },
			}
			list, err, fetched := c.Offered()
			switch {
			case !fetched:
				return append(rows, menu.Row{Label: say.T("home.loading")}), append(taps, nil)
			case err != nil:
				return append(rows,
						menu.Row{Label: say.T("home.failed"), Hint: err.Error()},
						menu.Row{Label: say.T("home.retry")},
					),
					append(taps, nil, func(int) { c.Fetch() })
			case len(list) == 0:
				return append(rows, menu.Row{Label: say.T("calendar.none")}), append(taps, nil)
			}
			editing := e.editing()
			for _, cal := range list {
				on := slices.Contains(c.Chosen(), cal.ID)
				row := menu.Row{
					Picture: e.swatch(cal.ID, on),
					Label:   cal.Name,
					Hint:    cal.ID,
					Kind:    menu.Toggle,
					On:      on,
					Chosen:  on && cal.ID == editing,
				}
				if on {
					row.Select = func() { e.pick(cal.ID) }
				}
				rows = append(rows, row)
				taps = append(taps, func(int) { c.Toggle(cal.ID) })
			}
			return rows, taps
		},
	}
}

type colorEditor struct {
	view *Renderer
	cal  *calendar.Calendar

	mu      sync.Mutex
	entity  string
	draft   gogui.HSLA
	drafted bool
	pending *time.Timer
}

func (e *colorEditor) editing() string {
	chosen := e.cal.Chosen()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !slices.Contains(chosen, e.entity) {
		e.entity, e.drafted = "", false
		if len(chosen) > 0 {
			e.entity = chosen[0]
		}
	}
	return e.entity
}

func (e *colorEditor) swatch(entity string, on bool) gogui.View {
	side := style.Reach() * 0.8
	if !on {
		return gogui.Row(gogui.ContainerCfg{
			Width: side, Height: side, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
		})
	}
	return widgets.Dot(e.shade(entity)).View(nil, side, side)
}

func (e *colorEditor) shade(entity string) theme.Color {
	e.mu.Lock()
	editing, draft, drafted := e.entity, e.draft, e.drafted
	e.mu.Unlock()
	if drafted && entity == editing {
		c := draft.Color()
		return theme.Color{R: c.R, G: c.G, B: c.B}
	}
	return e.view.options.UI.Palette().Roles.Or(e.cal.Color(entity))
}

func (e *colorEditor) side(w *gogui.Window) gogui.View {
	entity := e.editing()
	if entity == "" {
		return nil
	}
	vw, vh := w.WindowSize()
	reach := style.Reach()
	size := min(float32(min(vw, vh))*0.5, reach*8)
	pal := e.view.options.UI.Palette()
	gap := reach * 0.2
	side := min(
		reach*1.1,
		(size-gap*float32(schema.CalendarColors-1))/float32(schema.CalendarColors),
	)
	current := e.shade(entity)
	ring := gogui.CurrentTheme().Cfg.ColorAccent
	edge := max(reach*0.06, 2)
	var presets []gogui.View
	for slot := 1; slot <= schema.CalendarColors; slot++ {
		c := pal.Roles.At(slot)
		frame := gogui.Color{}
		if c == current {
			frame = ring
		}
		presets = append(presets, gogui.Row(gogui.ContainerCfg{
			Sizing:  gogui.FitFit,
			Padding: gogui.PadAll(edge),
			Radius:  gogui.RadiusLarge,
			Color:   frame,
			Content: []gogui.View{gogui.ColorSwatch(gogui.ColorSwatchCfg{
				ID:     fmt.Sprintf("calendar-preset-%d", slot),
				Color:  widgets.Color(c),
				Width:  side,
				Height: side,
				Radius: gogui.RadiusLarge,
				OnClick: func(ev gogui.EventCtx) {
					ev.Consume()
					e.save(c.String())
				},
			})},
		}))
	}
	value := gogui.ColorToHSLA(gogui.RGB(current.R, current.G, current.B))
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FitFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingPx(reach * 0.4),
		HAlign:  gogui.HAlignCenter,
		Content: []gogui.View{
			gogui.Row(gogui.ContainerCfg{
				Sizing: gogui.FitFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingPx(gap),
				Content: presets,
			}),
			gogui.ColorWheel(gogui.ColorWheelCfg{
				ID:            "calendar-wheel",
				Value:         gogui.HSLA{H: value.H, S: value.S, L: 0.5, A: 1},
				Size:          size,
				MarkerSize:    reach * 0.55,
				FocusDisabled: true,
				OnChange: func(next gogui.HSLA, ev gogui.EventCtx) {
					e.set(next)
					ev.Window.InvalidateLayout()
				},
			}),
		},
	})
}

func (e *colorEditor) pick(entity string) {
	e.flush()
	e.mu.Lock()
	e.entity, e.drafted = entity, false
	e.mu.Unlock()
	e.view.options.Shell.Redraw()
}

func (e *colorEditor) set(v gogui.HSLA) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.draft, e.drafted = v, true
	if e.pending != nil {
		e.pending.Stop()
	}
	e.pending = time.AfterFunc(colorSettle, e.flush)
}

func (e *colorEditor) flush() {
	e.mu.Lock()
	if e.pending == nil {
		e.mu.Unlock()
		return
	}
	e.pending.Stop()
	e.pending = nil
	entity, c := e.entity, e.draft.Color()
	e.mu.Unlock()
	e.cal.SetColor(entity, theme.Color{R: c.R, G: c.G, B: c.B}.String())
}

func (e *colorEditor) save(hex string) {
	e.mu.Lock()
	if e.pending != nil {
		e.pending.Stop()
		e.pending = nil
	}
	entity := e.entity
	e.drafted = false
	e.mu.Unlock()
	e.cal.SetColor(entity, hex)
}

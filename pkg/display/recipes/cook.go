package recipes

import (
	"image"
	"strconv"
	"strings"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/ui"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/mealie"
	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/voice"
)

func (a *App) cooking(w *gogui.Window) gogui.View {
	s, ok := a.o.Kitchen.Session()
	if !ok {
		return note(say.T("actions.recipes.closed"))
	}
	if len(s.Recipe.Steps) == 0 {
		return note(say.T("actions.recipes.nosteps"))
	}
	width := widgets.PageWidth(w)
	a.follow(w, s)
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		Content: []gogui.View{a.progress(s, width), a.stage(w, s), a.controls(s)},
	})
}

func (a *App) follow(w *gogui.Window, s mealie.Session) {
	a.mu.Lock()
	from, slug, size := a.shown, a.shownSlug, a.stageSize
	a.shown, a.shownSlug = s.Step, s.Recipe.Slug
	a.mu.Unlock()
	if slug != s.Recipe.Slug || from == s.Step || size.X <= 0 {
		return
	}
	to := s.Step
	w.QueueCommand(func(w *gogui.Window) { a.pager.Slid(w, from, to, to > from, float32(size.X)) })
}

func (a *App) stage(w *gogui.Window, s mealie.Session) gogui.View {
	a.mu.Lock()
	size := a.stageSize
	a.mu.Unlock()
	cfg := gogui.ContainerCfg{
		ID:          "recipes-cook-stage",
		Sizing:      gogui.FillFill,
		Padding:     gogui.NoPadding,
		Clip:        true,
		OnGesture:   a.swipe,
		AmendLayout: a.measure,
	}
	if size.X <= 0 || size.Y <= 0 {
		cfg.Content = []gogui.View{a.page(w, s, s.Step)}
		return gogui.Column(cfg)
	}
	slide := a.pager.Now()
	cfg.Content = []gogui.View{widgets.Placed(
		ui.Rect{X: int(slide.Offset), W: size.X, H: size.Y},
		a.page(w, s, s.Step),
	)}
	if slide.Moving() && slide.Other < len(s.Recipe.Steps) {
		cfg.Content = append(cfg.Content, widgets.Placed(
			ui.Rect{X: int(slide.OtherShift(float32(size.X))), W: size.X, H: size.Y},
			a.page(w, s, slide.Other),
		))
	}
	return gogui.Column(cfg)
}

func (a *App) measure(e gogui.EventCtx) {
	if e.Layout == nil || e.Layout.Shape == nil {
		return
	}
	size := image.Point{X: int(e.Layout.Shape.Width), Y: int(e.Layout.Shape.Height)}
	a.mu.Lock()
	changed := size != a.stageSize
	a.stageSize = size
	a.mu.Unlock()
	if changed {
		a.redraw()
	}
}

func (a *App) page(w *gogui.Window, s mealie.Session, at int) gogui.View {
	vw, vh := w.WindowSize()
	id := strconv.Itoa(at)
	big := gogui.CurrentTheme().Cfg.TextStyleDef
	big.Size *= 1.7
	text := gogui.Column(gogui.ContainerCfg{
		ID: "recipes-cook-text-" + id, Sizing: gogui.FillFill,
		Padding: gogui.NewPadding(0, style.Reach()*0.4, 0, 0),
		Spacing: gogui.SpacingMedium, Scrollable: true, OnGesture: a.swipe,
		Content: []gogui.View{wrap(mealie.Plain(s.Recipe.Steps[at].Text), big)},
	})
	need := a.need(s, at)
	if vw >= vh {
		return gogui.Row(gogui.ContainerCfg{
			Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingLarge,
			Content: []gogui.View{text, gogui.Column(gogui.ContainerCfg{
				ID: "recipes-cook-need-" + id, Width: widgets.PageWidth(w) * 0.34,
				Sizing: gogui.FixedFill, Padding: gogui.PaddingMedium, Radius: gogui.RadiusLarge,
				Scrollable: true, Color: gogui.CurrentTheme().Cfg.ColorPanel,
				OnGesture: a.swipe, Spacing: gogui.SpacingSmall, Content: need,
			})},
		})
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Spacing: gogui.SpacingLarge,
		Content: []gogui.View{text, gogui.Column(gogui.ContainerCfg{
			Sizing: gogui.FillFit, Padding: gogui.PaddingMedium, Radius: gogui.RadiusLarge,
			Color: gogui.CurrentTheme().Cfg.ColorPanel, Spacing: gogui.SpacingSmall,
			Content: need,
		})},
	})
}

func (a *App) neighbour(by int) int {
	s, ok := a.o.Kitchen.Session()
	if !ok {
		return -1
	}
	to := s.Step + by
	if to < 0 || to >= len(s.Recipe.Steps) {
		return -1
	}
	return to
}

func (a *App) turnTo(to int) int {
	s, _ := a.o.Kitchen.Session()
	a.mu.Lock()
	a.shown = to
	a.mu.Unlock()
	a.o.Kitchen.Goto(to)
	return s.Step
}

func (a *App) progress(s mealie.Session, width float32) gogui.View {
	step := s.Recipe.Steps[s.Step]
	label := say.F("recipes.step", map[string]any{"N": s.Step + 1, "Of": len(s.Recipe.Steps)})
	if t := strings.TrimSpace(step.Title); t != "" {
		label += " · " + t
	}
	n := len(s.Recipe.Steps)
	gap := float32(4)
	seg := max((width-gap*float32(n-1))/float32(n), 2)
	accent := gogui.CurrentTheme().Cfg.ColorAccent
	dim := gogui.CurrentTheme().Cfg.ColorPanel
	bars := make([]gogui.View, n)
	for i := range n {
		color := dim
		if i <= s.Step {
			color = accent
		}
		at := i
		bars[i] = a.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
			ID: "recipes-cook-bar-" + strconv.Itoa(i), Width: seg, Height: style.Reach() * 0.12,
			Sizing: gogui.FixedFixed, Padding: gogui.NoPadding, Radius: gogui.RadiusSmall,
			Color: color,
		}, func(e gogui.EventCtx) {
			a.o.Kitchen.Goto(at)
			e.Consume()
		})
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
		Content: []gogui.View{
			gogui.Label(label, widgets.Secondary()),
			gogui.Row(gogui.ContainerCfg{
				Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingPx(gap),
				Content: bars,
			}),
		},
	})
}

func (a *App) need(s mealie.Session, at int) []gogui.View {
	uses := s.Uses(at)
	title := say.T("recipes.need")
	if len(uses) == 0 {
		uses, title = s.Recipe.Ingredients, say.T("recipes.ingredients")
	}
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	views := []gogui.View{heading(title)}
	for _, in := range uses {
		if text := in.Scaled(s.Scale()); text != "" {
			views = append(views, gogui.Row(gogui.ContainerCfg{
				Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
				Content: []gogui.View{
					icon("mdi:circle-small", body.Size, widgets.Secondary().Color),
					wrap(text, body),
				},
			}))
		}
	}
	if d := s.Recipe.Steps[at].Duration(); d > 0 && at == s.Step {
		views = append(views, a.chip("recipes-cook-timer", "mdi:timer-outline",
			say.F("recipes.timer", map[string]any{"Time": duration(d)}), true,
			func() { a.o.Kitchen.StepTimer() }))
	}
	return views
}

func (a *App) controls(s mealie.Session) gogui.View {
	last := s.Step == len(s.Recipe.Steps)-1
	next := a.chip("recipes-cook-next", "mdi:arrow-right", say.T("recipes.next"), true,
		func() { a.o.Kitchen.Move(1) })
	if last {
		next = a.chip("recipes-cook-done", "mdi:check", say.T("recipes.done"), true, a.finish)
	}
	row := []gogui.View{
		a.chip("recipes-cook-back", "mdi:arrow-left", say.T("recipes.back"), false,
			func() { a.o.Kitchen.Move(-1) }),
		gogui.Row(gogui.ContainerCfg{Sizing: gogui.FillFit, Padding: gogui.NoPadding}),
	}
	if a.listening() {
		row = append(row, fact("mdi:microphone-outline", say.T("recipes.listening")))
	}
	row = append(row, next)
	return gogui.Row(gogui.ContainerCfg{
		Sizing: gogui.FillFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
		VAlign: gogui.VAlignMiddle, Content: row,
	})
}

func (a *App) swipe(e gogui.EventCtx) {
	ev := e.Event
	a.mu.Lock()
	width := float32(a.stageSize.X)
	a.mu.Unlock()
	if ev == nil || width <= 0 {
		a.o.UI.HoldStill(e)
		return
	}
	switch ev.GestureType {
	case gogui.GesturePan:
		switch ev.GesturePhase {
		case gogui.GesturePhaseBegan, gogui.GesturePhaseChanged:
			if !a.sideways(ev.GestureDX, ev.GestureDY) {
				a.o.UI.HoldStill(e)
				return
			}
			a.pager.Drag(e.Window, ev.GestureDX, width, a.neighbour, a.turnTo)
		default:
			held := a.release()
			if !held {
				a.o.UI.HoldStill(e)
				return
			}
			a.pager.Lift(e.Window, 0)
		}
	case gogui.GestureSwipe:
		held := a.release()
		if !held && ev.VelocityX*ev.VelocityX <= ev.VelocityY*ev.VelocityY {
			a.o.UI.HoldStill(e)
			return
		}
		a.pager.Lift(e.Window, ev.VelocityX)
	default:
		a.o.UI.HoldStill(e)
		return
	}
	e.Consume()
	e.Window.InvalidateLayout()
}

func (a *App) sideways(dx, dy float32) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.panAxis == panUndecided && (dx != 0 || dy != 0) {
		a.panAxis = panVertical
		if dx*dx > dy*dy {
			a.panAxis = panSideways
		}
	}
	return a.panAxis == panSideways
}

func (a *App) release() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	held := a.panAxis == panSideways
	a.panAxis = panUndecided
	return held
}

func (a *App) finish() {
	a.o.Kitchen.Done()
	a.o.Shell.Remove(a.cook)
}

func (a *App) listening() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hush != nil && a.o.Voice.Allowed(mealie.CookCommand)
}

func (a *App) listen(on bool) {
	if a.o.Voice == nil {
		return
	}
	a.mu.Lock()
	hush := a.hush
	if on == (hush != nil) {
		a.mu.Unlock()
		return
	}
	a.hush = nil
	a.mu.Unlock()
	if !on {
		hush()
		return
	}
	cancel := a.o.Voice.Listen(voice.Hook{
		Name: mealie.CookCommand,
		Keywords: []string{
			"next step",
			"previous step",
			"go back",
			"start timer",
			"start the timer",
		},
		Heard: a.heard,
	})
	a.mu.Lock()
	a.hush = cancel
	a.mu.Unlock()
}

func (a *App) heard(h voice.Heard) {
	switch {
	case strings.Contains(h.Text, "next step"):
		a.o.Kitchen.Move(1)
	case strings.Contains(h.Text, "previous step"), strings.Contains(h.Text, "go back"):
		a.o.Kitchen.Move(-1)
	case strings.Contains(h.Text, "timer"):
		a.o.Kitchen.StepTimer()
	}
}

package screens

import (
	"log/slog"
	"strconv"
	"sync"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/ui"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
)

const (
	slideTime = 280 * time.Millisecond
	leavingID = "leaving"
	pageID    = "page"
)

type move struct {
	from   *Screen
	back   bool
	at     float32
	n      int
	frames int
	began  time.Time
}

type mover struct {
	mu     sync.Mutex
	asked  *shell.Change
	shown  *Screen
	drawn  bool
	moving *move
	count  int
}

func (a *App) changed(c shell.Change) {
	a.moves.mu.Lock()
	a.moves.asked = &c
	a.moves.mu.Unlock()
}

func (a *App) transition(w *gogui.Window, covering *Screen) gogui.View {
	m := &a.moves
	m.mu.Lock()
	asked, from, drawn := m.asked, m.shown, m.drawn
	m.shown, m.drawn = covering, true
	opened := asked != nil && opens(*asked, covering)
	if opened {
		m.asked = nil
	}
	m.mu.Unlock()
	if drawn && from != covering && opened {
		a.start(w, from, covering, *asked)
	}
	m.mu.Lock()
	mv := m.moving
	var now move
	if mv != nil {
		mv.frames++
		now = *mv
	}
	m.mu.Unlock()
	if mv == nil {
		return a.base(w, covering)
	}
	return a.between(w, now, covering)
}

func opens(c shell.Change, covering *Screen) bool {
	if covering == nil {
		return c.To == nil || !c.To.Covers()
	}
	return c.To == covering.View
}

func (a *App) start(w *gogui.Window, from, to *Screen, c shell.Change) {
	m := &a.moves
	m.mu.Lock()
	if c.Idle || a.outside(from) || a.outside(to) {
		m.moving = nil
		m.mu.Unlock()
		return
	}
	m.count++
	mv := &move{from: from, back: c.Back, n: m.count, began: time.Now()}
	m.moving = mv
	m.mu.Unlock()
	if from != nil && from.Title != "" {
		offset := w.ScrollVerticalOffset(pageID)
		w.ScrollVerticalTo(leavingID+gogui.IDSep+pageID, offset)
	}
	tw := gogui.NewTweenAnimation(
		"screen-move-"+strconv.Itoa(mv.n),
		0,
		1,
		func(v float32, w *gogui.Window) {
			m.mu.Lock()
			mv.at = v
			m.mu.Unlock()
		},
	)
	tw.Duration, tw.Easing = slideTime, gogui.EaseInOutCubic
	tw.OnDone = func(w *gogui.Window) {
		m.mu.Lock()
		if m.moving == mv {
			m.moving = nil
		}
		frames, took := mv.frames, time.Since(mv.began)
		m.mu.Unlock()
		slog.Debug("screen slide", "back", c.Back, "frames", frames, "took", took,
			"fps", float64(frames)/took.Seconds())
		w.InvalidateLayout()
	}
	w.AnimationAdd(tw)
}

func (a *App) outside(s *Screen) bool {
	if s != nil {
		return s.Clear
	}
	return a.lovelace() || a.o.Firmware != nil && a.o.Firmware.On() || a.papered.Load()
}

func (a *App) between(w *gogui.Window, mv move, to *Screen) gogui.View {
	vw, vh := w.WindowSize()
	ground := gogui.CurrentTheme().Cfg.ColorBackground
	leaving := gogui.Column(gogui.ContainerCfg{
		ID: leavingID, Sizing: gogui.FillFill, Padding: gogui.NoPadding, Color: ground,
		Content: []gogui.View{a.base(w, mv.from)},
	})
	staying := gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Color: ground,
		Content: []gogui.View{a.base(w, to)},
	})
	under, over, t := leaving, staying, mv.at
	if mv.back {
		under, over, t = staying, leaving, 1-mv.at
	}
	width := float32(vw)
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.NoPadding, Clip: true,
		Content: []gogui.View{
			widgets.Placed(ui.Rect{X: int(-t * width), W: vw, H: vh}, under),
			widgets.Placed(ui.Rect{X: int((1 - t) * width), W: vw, H: vh}, over),
		},
	})
}

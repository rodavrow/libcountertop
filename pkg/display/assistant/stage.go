package assistant

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	voice "github.com/ygelfand/libcountertop/pkg/assistant/turn"
	"github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/display/gpu"
	"github.com/ygelfand/libcountertop/pkg/display/livevisual"
	"github.com/ygelfand/libcountertop/pkg/display/ui"
	"github.com/ygelfand/libcountertop/pkg/display/visual"
	"github.com/ygelfand/libcountertop/pkg/display/visualview"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const (
	aboveUI     = 6
	belowUI     = 1
	stageEvery  = time.Second / 60
	stageLinger = time.Second
	stageGrace  = 200 * time.Millisecond
	stageHide   = 150 * time.Millisecond
	stageRetire = 3
)

type Visuals interface {
	Hold() (release func())
	Input() visual.Input
}

type WakeSettings interface {
	Read() schema.Wake
}

// AvatarScreen is a wake word's avatar video, shown in its look's place while its picture is up.
type AvatarScreen interface {
	Wants(slot int) bool
	// Showing readies the picture and reports whether it is up.
	Showing(slot int) bool
	// Place shows the picture in a box in screen coordinates, over the interface (above) for a
	// look in a panel or under it for one across the screen.
	Place(slot int, box geometry.Rect, above bool) bool
	// Pending reports that the picture is being readied and is due in a moment, so the look's
	// place stays empty rather than flash the normal look first.
	Pending(slot int) bool
	// Held reports the avatar kept on screen between announcements, if any.
	Held() (slot int, ok bool)
	// Release lets the held avatar go and reports whether one was held.
	Release() bool
	// Began says slot's turn or announcement is on screen, taking a held avatar's place.
	Began(slot int)
}

type StageDependencies struct {
	Screen  *Assistant
	GPU     livevisual.Layers
	Display livevisual.Oriented
	Visuals Visuals
	Wake    WakeSettings
	// Avatar is nil on boards without avatars.
	Avatar AvatarScreen
}

type look struct {
	place schema.LookPlace
	stage schema.Stage
	kind  string
}

func (l look) custom() bool { return l.kind != "" }

func stageOf(p voice.Phase) (schema.Stage, bool) {
	switch p {
	case voice.Listening:
		return schema.StageListening, true
	case voice.Thinking:
		return schema.StageWaiting, true
	case voice.Replying, voice.Idle:
		return schema.StageResponding, true
	}
	return "", false
}

type wanted struct {
	at     ui.Rect
	z      int
	kind   string
	source schema.Source
}

type Stage struct {
	deps StageDependencies

	shown atomic.Bool
	full  atomic.Bool

	// avatarAt is when the avatar's picture was last placed, in unix nanoseconds.
	avatarAt   atomic.Int64
	avatarFull atomic.Bool

	mu      sync.Mutex
	want    ui.Rect
	z       int
	kind    string
	source  schema.Source
	seen    time.Time
	running bool

	cur      *livevisual.Slot
	next     *livevisual.Slot
	retiring *livevisual.Slot
	retireIn int
	broken   string
	release  func()
	hiding   time.Time
}

func NewStage(deps StageDependencies) *Stage { return &Stage{deps: deps} }

func (s *Stage) lookFor(show Showing) look {
	st, ok := stageOf(show.Phase)
	if !ok {
		return look{}
	}
	l := s.deps.Wake.Read().Slot(show.Slot).Look
	return look{place: l.Place, stage: st, kind: l.Kind(st)}
}

func (s *Stage) Look(show Showing) (custom, full bool) {
	l := s.lookFor(show)
	if s.avatarUp(show) || s.avatarPending(show) {
		return true, l.place == schema.LookFull
	}
	return l.custom(), l.place == schema.LookFull
}

func (s *Stage) Above(at ui.Rect, show Showing) {
	if !s.avatar(at, false, show) {
		s.stage(at, aboveUI, s.lookFor(show))
	}
}

func (s *Stage) Below(at ui.Rect, show Showing) {
	if !s.avatar(at, true, show) {
		s.stage(at, belowUI, s.lookFor(show))
	}
}

func (s *Stage) Staged() bool { return s.shown.Load() || s.avatarShown() }

func (s *Stage) StagedFull() bool {
	if s.avatarShown() {
		return s.avatarFull.Load()
	}
	return s.shown.Load() && s.full.Load()
}

// avatarUp reports whether show's wake word has its avatar picture up, readying it if not; the
// look is drawn as usual until it is.
func (s *Stage) avatarUp(show Showing) bool {
	a := s.deps.Avatar
	if a == nil {
		return false
	}
	if _, ok := stageOf(show.Phase); !ok || !a.Wants(show.Slot) {
		return false
	}
	return a.Showing(show.Slot)
}

// avatarPending reports that show's wake word has its avatar picture on its way.
func (s *Stage) avatarPending(show Showing) bool {
	a := s.deps.Avatar
	if a == nil {
		return false
	}
	if _, ok := stageOf(show.Phase); !ok || !a.Wants(show.Slot) {
		return false
	}
	return a.Pending(show.Slot)
}

// avatar puts the avatar's picture where the look would be, in place of the GPU visual. While
// the picture is on its way it keeps the place empty, so the normal look does not flash first.
func (s *Stage) avatar(at ui.Rect, full bool, show Showing) bool {
	if at.W <= 0 || at.H <= 0 {
		return false
	}
	up := s.avatarUp(show)
	if !up && !s.avatarPending(show) {
		return false
	}
	box := geometry.Rect{X: at.X, Y: at.Y, W: at.W, H: at.H}
	if up && !s.deps.Avatar.Place(show.Slot, box, !full) && !s.avatarPending(show) {
		return false
	}
	s.Unstage()
	s.avatarAt.Store(time.Now().UnixNano())
	s.avatarFull.Store(full)
	return true
}

// Held is what to show for the avatar kept on screen between announcements: its wake word,
// idle, with no captions.
func (s *Stage) Held() (Showing, bool) {
	a := s.deps.Avatar
	if a == nil {
		return Showing{}, false
	}
	slot, ok := a.Held()
	if !ok {
		return Showing{}, false
	}
	idle := voice.Showing{Phase: voice.Idle, Slot: slot}
	return Showing{Showing: idle, Down: 1, At: time.Now()}, true
}

// Began says slot's turn or announcement is on screen; a held avatar waiting for it goes.
func (s *Stage) Began(slot int) {
	if s.deps.Avatar != nil {
		s.deps.Avatar.Began(slot)
	}
}

// Release lets the avatar held between announcements go, as a touch does, and reports whether
// one was held.
func (s *Stage) Release() bool { return s.deps.Avatar != nil && s.deps.Avatar.Release() }

func (s *Stage) avatarShown() bool {
	at := s.avatarAt.Load()
	return at != 0 && time.Since(time.Unix(0, at)) <= stageGrace
}

func (s *Stage) stage(at ui.Rect, z int, l look) {
	if !l.custom() || at.W <= 0 || at.H <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.want, s.z, s.kind, s.source, s.seen = at, z, l.kind, l.stage.Source(), time.Now()
	if !s.running {
		s.running = true
		go s.run()
	}
}

func (s *Stage) Unstage() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		s.seen = time.Time{}
	}
}

func (s *Stage) run() {
	t := time.NewTicker(stageEvery)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		linger := stageLinger
		if s.z != belowUI {
			linger = stageGrace
		}
		if time.Since(s.seen) <= linger {
			s.hiding = time.Time{}
		} else if s.z != belowUI {
			s.close()
			s.running = false
			s.mu.Unlock()
			return
		} else if s.hiding.IsZero() {
			s.hiding = time.Now()
			s.shown.Store(false)
			s.deps.Screen.Hide()
		} else if time.Since(s.hiding) > stageHide {
			s.close()
			s.hiding = time.Time{}
			s.running = false
			s.mu.Unlock()
			return
		}
		w := wanted{at: s.want, z: s.z, kind: s.kind, source: s.source}
		hiding := !s.hiding.IsZero()
		s.mu.Unlock()
		s.frame(w, hiding)
	}
}

func (s *Stage) frame(w wanted, hiding bool) {
	if s.release == nil {
		s.release = s.deps.Visuals.Hold()
	}
	if s.cur != nil && !s.cur.Fits(w.at, w.z) {
		s.drop()
	}
	if s.cur == nil {
		if w.kind == s.broken {
			return
		}
		if s.cur = s.open(w, w.z, w.at); s.cur == nil {
			return
		}
		s.full.Store(w.z == belowUI)
	}

	in := visualview.From(s.deps.Visuals.Input(), w.source)
	if !s.shade(s.cur, w.at, w.at, in) {
		s.drop()
		return
	}
	if !hiding && !s.shown.Load() {
		s.shown.Store(true)
		s.deps.Screen.Paint()
	}
	if s.retiring != nil {
		if s.retireIn--; s.retireIn <= 0 {
			s.retiring.Close()
			s.retiring = nil
		}
	}

	if s.cur.Kind == w.kind || w.kind == s.broken {
		s.next.Close()
		s.next = nil
		return
	}
	if s.next != nil && (s.next.Kind != w.kind || !s.next.Fits(w.at, w.z)) {
		s.next.Close()
		s.next = nil
	}
	off := w.at
	off.X += off.W * 2
	if s.next == nil {
		z := w.z
		if s.cur.Depth == w.z {
			z = w.z + 1
		}
		if s.next = s.open(w, z, off); s.next == nil {
			return
		}
	}
	if !s.shade(s.next, off, w.at, in) {
		s.next.Close()
		s.next = nil
		return
	}
	if !s.place(s.next, w.at) {
		s.next.Close()
		s.next = nil
		return
	}
	s.retiring.Close()
	s.retiring, s.retireIn = s.cur, stageRetire
	s.cur, s.next = s.next, nil
}

func (s *Stage) open(w wanted, z int, at ui.Rect) *livevisual.Slot {
	sl, err := livevisual.Open(s.deps.GPU, w.kind, w.at, w.z, z)
	if errors.Is(err, gpu.ErrNoHelper) {
		return nil
	}
	if err != nil {
		slog.Error("assistant: opening the GPU layer", "err", err)
		s.broken = w.kind
		return nil
	}
	if !s.place(sl, at) {
		sl.Close()
		return nil
	}
	return sl
}

func (s *Stage) place(sl *livevisual.Slot, at ui.Rect) bool {
	if err := sl.Place(s.deps.Display, at); err != nil {
		slog.Error("assistant: placing the GPU layer", "err", err)
		return false
	}
	return true
}

func (s *Stage) shade(sl *livevisual.Slot, placed, area ui.Rect, in visual.Input) bool {
	if err := sl.Shade(s.deps.Display, placed, area, in); err != nil {
		slog.Error("assistant: GPU visual failed", "kind", sl.Kind, "alive", sl.Alive(), "err", err)
		if sl.Alive() {
			s.broken = sl.Kind
		}
		return false
	}
	return true
}

func (s *Stage) drop() {
	s.shown.Store(false)
	s.cur.Close()
	s.next.Close()
	s.retiring.Close()
	s.cur, s.next, s.retiring = nil, nil, nil
}

func (s *Stage) close() {
	s.drop()
	if s.release != nil {
		s.release()
		s.release = nil
	}
}

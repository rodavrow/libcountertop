package assistant

import (
	"testing"

	"github.com/ygelfand/libcountertop/pkg/assistant/avatar"
	voice "github.com/ygelfand/libcountertop/pkg/assistant/turn"
	"github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/display/ui"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type plainLook struct{ place schema.LookPlace }

func (p plainLook) Read() schema.Wake {
	w := schema.DefaultWakeWord()
	w.Look.Place = p.place
	return schema.Wake{Words: []schema.WakeWord{w, w}}
}

type fakeAvatar struct {
	wants, up, pending bool
	placed             []geometry.Rect
	above              []bool
	primed             int
	held               int
	released           bool
}

func (a *fakeAvatar) Wants(int) bool { return a.wants }
func (a *fakeAvatar) Showing(int) bool {
	a.primed++
	return a.up
}
func (a *fakeAvatar) Pending(int) bool { return a.pending }
func (a *fakeAvatar) Held() (int, bool) {
	if a.held == 0 {
		return -1, false
	}
	return a.held - 1, true
}
func (a *fakeAvatar) Release() bool {
	had := a.held != 0
	a.held, a.released = 0, true
	return had
}
func (a *fakeAvatar) Place(_ int, box geometry.Rect, above bool) bool {
	a.placed, a.above = append(a.placed, box), append(a.above, above)
	return a.up
}

func replying(slot int) Showing {
	return Showing{Showing: voice.Showing{Phase: voice.Replying, Slot: slot}}
}

func TestTheNormalLookShowsUntilTheAvatarHasAPicture(t *testing.T) {
	av := &fakeAvatar{wants: true}
	s := NewStage(StageDependencies{Wake: plainLook{schema.LookPanel}, Avatar: av})

	custom, _ := s.Look(replying(1))

	if custom || av.primed == 0 {
		t.Fatalf("custom %v, primed %d: want the normal look while the picture readies", custom, av.primed)
	}
}

func TestTheAvatarTakesTheLooksPlaceOnceItHasAPicture(t *testing.T) {
	av := &fakeAvatar{wants: true, up: true}
	s := NewStage(StageDependencies{Wake: plainLook{schema.LookFull}, Avatar: av})
	box := ui.Rect{X: 0, Y: 0, W: 1280, H: 800}

	custom, full := s.Look(replying(1))
	s.Below(box, replying(1))

	if !custom || !full {
		t.Fatalf("custom %v, full %v", custom, full)
	}
	if len(av.placed) != 1 || av.placed[0] != (geometry.Rect{W: 1280, H: 800}) || av.above[0] {
		t.Fatalf("placed %+v", av.placed)
	}
	if !s.Staged() || !s.StagedFull() {
		t.Fatalf("staged %v, full %v", s.Staged(), s.StagedFull())
	}
	if s.running {
		t.Fatal("a GPU visual runs under the avatar")
	}
}

func TestAnAvatarInAPanelSitsOverTheInterface(t *testing.T) {
	av := &fakeAvatar{wants: true, up: true}
	s := NewStage(StageDependencies{Wake: plainLook{schema.LookPanel}, Avatar: av})

	s.Above(ui.Rect{X: 100, Y: 500, W: 400, H: 250}, replying(1))

	if len(av.above) != 1 || !av.above[0] {
		t.Fatalf("placed above %v, want over the interface", av.above)
	}
}

func TestAWakeWordWithoutAnAvatarKeepsItsLook(t *testing.T) {
	av := &fakeAvatar{up: true}
	s := NewStage(StageDependencies{Wake: plainLook{schema.LookPanel}, Avatar: av})

	custom, _ := s.Look(replying(0))
	s.Above(ui.Rect{W: 10, H: 10}, replying(0))

	if custom || len(av.placed) != 0 || av.primed != 0 {
		t.Fatalf("custom %v, placed %v, primed %d", custom, av.placed, av.primed)
	}
}

func TestNoAvatarIsShownWhenIdle(t *testing.T) {
	av := &fakeAvatar{wants: true, up: true}
	s := NewStage(StageDependencies{Wake: plainLook{schema.LookPanel}, Avatar: av})

	idle := Showing{Showing: voice.Showing{Phase: voice.Phase(99)}}
	if custom, _ := s.Look(idle); custom {
		t.Fatal("an avatar shows outside a turn")
	}
}

// A full-screen avatar takes the full-screen look's place, so it must sit at its depth: any
// lower and the photo screen's GPU layer covers it.
func TestAFullScreenAvatarSitsAtTheFullScreenLooksDepth(t *testing.T) {
	if avatar.DefaultZ != belowUI {
		t.Fatalf("avatar depth %d, full-screen look depth %d", avatar.DefaultZ, belowUI)
	}
}

func TestAPendingAvatarLeavesTheLooksPlaceEmptyInsteadOfTheNormalLook(t *testing.T) {
	av := &fakeAvatar{wants: true, pending: true}
	s := NewStage(StageDependencies{Wake: plainLook{schema.LookFull}, Avatar: av})

	custom, full := s.Look(replying(1))
	s.Below(ui.Rect{W: 1280, H: 800}, replying(1))

	if !custom || !full {
		t.Fatalf("custom %v, full %v: want the avatar's place kept", custom, full)
	}
	if s.running {
		t.Fatal("the normal look's GPU visual runs while the avatar readies")
	}
	if !s.Staged() || !s.StagedFull() {
		t.Fatalf("staged %v, full %v: the screen behind shows while the avatar readies",
			s.Staged(), s.StagedFull())
	}
}

func TestAnAvatarPendingInAPanelDrawsNoNormalLook(t *testing.T) {
	av := &fakeAvatar{wants: true, pending: true}
	s := NewStage(StageDependencies{Wake: plainLook{schema.LookPanel}, Avatar: av})

	s.Above(ui.Rect{X: 100, Y: 500, W: 400, H: 250}, replying(1))

	if s.running {
		t.Fatal("the normal look's GPU visual runs while the avatar readies")
	}
}

func TestTheHeldAvatarShowsIdleWithNoCaptions(t *testing.T) {
	av := &fakeAvatar{held: 2}
	s := NewStage(StageDependencies{Wake: plainLook{schema.LookFull}, Avatar: av})

	show, ok := s.Held()

	if !ok || show.Slot != 1 || show.Phase != voice.Idle {
		t.Fatalf("held %+v %v, want wake word 2 idle", show, ok)
	}
	if show.Said != "" || show.Reply != "" || show.Speaker.Name != "" {
		t.Errorf("captions on a held avatar: %+v", show)
	}
	if !s.Release() || !av.released {
		t.Error("release did not reach the avatar")
	}
	if _, ok := s.Held(); ok {
		t.Error("still held after release")
	}
}

func TestNothingIsHeldWithoutAvatars(t *testing.T) {
	s := NewStage(StageDependencies{Wake: plainLook{schema.LookFull}})

	if _, ok := s.Held(); ok || s.Release() {
		t.Fatal("held without avatars")
	}
}

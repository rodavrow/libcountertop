package screens

import (
	"testing"

	voice "github.com/ygelfand/libcountertop/pkg/assistant/turn"
	"github.com/ygelfand/libcountertop/pkg/display/assistant"
	"github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/display/ui"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

// heldStage holds a full-screen avatar and has staged nothing lately, as between the screen's
// once-a-second redraws.
type heldStage struct{ released, began, beganSlot int }

func (s *heldStage) Above(ui.Rect, assistant.Showing)  {}
func (s *heldStage) Below(ui.Rect, assistant.Showing)  {}
func (s *heldStage) Unstage()                          {}
func (s *heldStage) Staged() bool                      { return false }
func (s *heldStage) StagedFull() bool                  { return false }
func (s *heldStage) AvatarLook(assistant.Showing) bool { return true }
func (s *heldStage) Look(assistant.Showing) (bool, bool) {
	return true, true
}
func (s *heldStage) Held() (assistant.Showing, bool) {
	return assistant.Showing{Showing: voice.Showing{Phase: voice.Idle}, Down: 1}, true
}
func (s *heldStage) Release() bool  { s.released++; return true }
func (s *heldStage) Began(slot int) { s.began, s.beganSlot = s.began+1, slot }

func TestAHeldFullScreenAvatarKeepsTheScreenSeeThrough(t *testing.T) {
	a := &App{o: Options{Assistant: assistant.New(assistant.Dependencies{}), Stage: &heldStage{}}}

	if !a.fullLook() {
		t.Fatal("the interface is drawn over a held full-screen avatar")
	}
}

// A show on screen tells the stage it began and never lets the hold go itself: the avatars know
// whether the hold was made under this show or is waiting for it.
func TestAShowOnScreenTellsTheStageItBegan(t *testing.T) {
	st := &heldStage{}
	as := assistant.New(assistant.Dependencies{})
	as.Show(voice.Showing{Phase: voice.Thinking, Slot: 1})
	a := &App{o: Options{Assistant: as, Stage: st}}

	if _, ok := a.showing(); !ok || st.released != 0 || st.began == 0 || st.beganSlot != 1 {
		t.Fatalf("showing %v, released %d, began %d for slot %d",
			ok, st.released, st.began, st.beganSlot)
	}
}

func TestAHeldAvatarWithNoShowHasNotBegunOne(t *testing.T) {
	st := &heldStage{}
	a := &App{o: Options{Assistant: assistant.New(assistant.Dependencies{}), Stage: st}}

	if _, ok := a.showing(); !ok || st.began != 0 {
		t.Fatalf("showing %v, began %d", ok, st.began)
	}
}

// fullWake gives every wake word a full-screen look, with a GPU visual when kind is set.
type fullWake struct{ kind string }

func (f fullWake) Read() schema.Wake {
	w := schema.DefaultWakeWord()
	w.Look = schema.Look{
		Place:      schema.LookFull,
		Listening:  f.kind,
		Waiting:    f.kind,
		Responding: f.kind,
	}
	return schema.Wake{Words: []schema.WakeWord{w, w}}
}

// cutAvatar is the next show's avatar at the cut from a held one: its picture up or on its way,
// and not placed by the screen yet.
type cutAvatar struct{ up bool }

func (c cutAvatar) Wants(int) bool                      { return true }
func (c cutAvatar) Showing(int) bool                    { return c.up }
func (c cutAvatar) Place(int, geometry.Rect, bool) bool { return c.up }
func (c cutAvatar) Pending(int) bool                    { return !c.up }
func (c cutAvatar) Held() (int, bool)                   { return -1, false }
func (c cutAvatar) Release() bool                       { return false }
func (c cutAvatar) Began(int)                           {}

func replyingApp(st *assistant.Stage) *App {
	as := assistant.New(assistant.Dependencies{})
	as.Show(voice.Showing{Phase: voice.Replying, Slot: 1})
	return &App{o: Options{Assistant: as, Stage: st}}
}

// On the first frame of the show after a held avatar, the screen has placed nothing within the
// stage's grace, as a held avatar is placed only at the slower redraws. The interface must not be
// drawn over the avatar for that frame.
func TestRegressionTheFirstFrameAfterAHeldAvatarKeepsTheScreenSeeThrough(t *testing.T) {
	for _, up := range []bool{true, false} {
		deps := assistant.StageDependencies{Wake: fullWake{}, Avatar: cutAvatar{up}}
		st := assistant.NewStage(deps)

		if !replyingApp(st).fullLook() {
			t.Errorf("picture up %v: the interface is drawn over the next show's avatar", up)
		}
	}
}

// A full-screen GPU visual still waits until it is on screen, so the screen is not see-through
// over nothing.
func TestAFullScreenVisualNotYetStagedDrawsTheInterface(t *testing.T) {
	st := assistant.NewStage(assistant.StageDependencies{Wake: fullWake{kind: "orb"}})

	if replyingApp(st).fullLook() {
		t.Fatal("see-through before the visual is on screen")
	}
}

package screens

import (
	"testing"

	voice "github.com/ygelfand/libcountertop/pkg/assistant/turn"
	"github.com/ygelfand/libcountertop/pkg/display/assistant"
	"github.com/ygelfand/libcountertop/pkg/display/ui"
)

// heldStage holds a full-screen avatar and has staged nothing lately, as between the screen's
// once-a-second redraws.
type heldStage struct{ released, began, beganSlot int }

func (s *heldStage) Above(ui.Rect, assistant.Showing) {}
func (s *heldStage) Below(ui.Rect, assistant.Showing) {}
func (s *heldStage) Unstage()                         {}
func (s *heldStage) Staged() bool                     { return false }
func (s *heldStage) StagedFull() bool                 { return false }
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

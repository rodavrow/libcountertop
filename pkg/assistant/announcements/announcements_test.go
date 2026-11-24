package announcements

import (
	"slices"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func fixture(on bool) (*Announcements, *clock, *bool) {
	saved := on
	c := &clock{at: time.Unix(1_800_000_000, 0)}
	a := New(Options{
		Slots: 2,
		Read:  func() bool { return saved },
		Save:  func(v bool) error { saved = v; return nil },
	})
	a.now = c.now
	return a, c, &saved
}

func choose(a *Announcements, option string) { a.next.OnCommand(option) }

func TestTheSelectOffersDefaultAndEveryAssistant(t *testing.T) {
	a, _, _ := fixture(true)

	want := []string{"Default", "Assistant 1", "Assistant 2"}
	if !slices.Equal(a.next.Options, want) {
		t.Fatalf("options %v, want %v", a.next.Options, want)
	}
	if a.next.ObjectID != NextID || a.next.Get() != Default {
		t.Fatalf("entity %q shows %q", a.next.ObjectID, a.next.Get())
	}
}

func TestWithNothingChosenTheFirstAssistantSpeaks(t *testing.T) {
	a, _, _ := fixture(true)

	if got := a.Slot(); got != 0 {
		t.Fatalf("slot %d, want 0", got)
	}
}

func TestAChosenAssistantSpeaksOnceThenTheFirstAgain(t *testing.T) {
	a, _, _ := fixture(true)

	choose(a, "Assistant 2")
	if got := a.next.Get(); got != "Assistant 2" {
		t.Fatalf("select shows %q after choosing", got)
	}
	if got := a.Slot(); got != 1 {
		t.Fatalf("first announcement spoke as slot %d, want 1", got)
	}
	if got := a.next.Get(); got != Default {
		t.Fatalf("select shows %q after the announcement, want Default", got)
	}
	if got := a.Slot(); got != 0 {
		t.Fatalf("second announcement spoke as slot %d, want 0", got)
	}
}

func TestAnAssistantThatDoesNotExistIsTheFirst(t *testing.T) {
	a, _, _ := fixture(true)

	for _, option := range []string{"Assistant 3", "Assistant 0", "nobody", ""} {
		choose(a, option)
		if got := a.Slot(); got != 0 {
			t.Errorf("%q spoke as slot %d, want 0", option, got)
		}
		if got := a.next.Get(); got != Default {
			t.Errorf("%q left the select on %q", option, got)
		}
	}
}

func TestChoosingDefaultTakesAChoiceBack(t *testing.T) {
	a, _, _ := fixture(true)

	choose(a, "Assistant 2")
	choose(a, Default)

	if got := a.Slot(); got != 0 {
		t.Fatalf("slot %d, want 0", got)
	}
}

func TestAChoiceNobodyUsesIsForgotten(t *testing.T) {
	a, c, _ := fixture(true)

	choose(a, "Assistant 2")
	c.at = c.at.Add(Kept)
	a.expire()

	if got := a.next.Get(); got != Default {
		t.Fatalf("select shows %q after the choice ran out, want Default", got)
	}
	if got := a.Slot(); got != 0 {
		t.Fatalf("slot %d, want 0", got)
	}
}

func TestAChoiceThatRanOutIsNotUsedEvenBeforeItIsCleared(t *testing.T) {
	a, c, _ := fixture(true)

	choose(a, "Assistant 2")
	c.at = c.at.Add(Kept)

	if got := a.Slot(); got != 0 {
		t.Fatalf("slot %d, want 0", got)
	}
}

func TestAChoiceStillInTimeOutlivesAnEarlierChoicesTimer(t *testing.T) {
	a, c, _ := fixture(true)

	choose(a, "Assistant 1")
	c.at = c.at.Add(Kept - time.Second)
	choose(a, "Assistant 2")
	c.at = c.at.Add(2 * time.Second)
	a.expire()

	if got := a.Slot(); got != 1 {
		t.Fatalf("slot %d, want 1", got)
	}
}

func TestTheSwitchReadsAndSavesTheSetting(t *testing.T) {
	a, _, saved := fixture(true)

	entities := a.Entities()
	if len(entities) != 2 || entities[1] != esphome.Entity(a.next) {
		t.Fatalf("entities %+v, want the switch and the select", entities)
	}
	sw, ok := entities[0].(*esphome.Switch)
	if !ok || sw.ObjectID != "assistants_animate_announcements" || !sw.Get() {
		t.Fatalf("entity %+v, want the switch, on", entities[0])
	}

	sw.OnCommand(false)
	if *saved || a.Animated() || a.On() {
		t.Fatal("switching off did not save")
	}
	if err := a.Set(true); err != nil || !*saved || !sw.Get() {
		t.Fatalf("Set(true): saved %v, switch %v, err %v", *saved, sw.Get(), err)
	}
}

type told struct {
	slot int
	on   bool
}

func listening(on bool) (*Announcements, *clock, *[]told) {
	a, c, _ := fixture(on)
	heard := &[]told{}
	a.options.Chosen = func(slot int, on bool) { *heard = append(*heard, told{slot, on}) }
	return a, c, heard
}

func TestUsingAChoiceTellsChosenOnThenOffOnce(t *testing.T) {
	a, _, heard := listening(true)

	choose(a, "Assistant 2")
	a.Slot()
	a.Slot()

	if want := []told{{1, true}, {1, false}}; !slices.Equal(*heard, want) {
		t.Fatalf("told %v, want %v", *heard, want)
	}
}

func TestChangingTheChoiceReleasesTheFirst(t *testing.T) {
	a, _, heard := listening(true)

	choose(a, "Assistant 1")
	choose(a, "Assistant 2")
	choose(a, Default)

	want := []told{{0, true}, {0, false}, {1, true}, {1, false}}
	if !slices.Equal(*heard, want) {
		t.Fatalf("told %v, want %v", *heard, want)
	}
}

func TestAnExpiredChoiceIsReleased(t *testing.T) {
	a, c, heard := listening(true)

	choose(a, "Assistant 2")
	c.at = c.at.Add(Kept + time.Second)
	a.expire()
	a.Slot()

	if want := []told{{1, true}, {1, false}}; !slices.Equal(*heard, want) {
		t.Fatalf("told %v, want %v", *heard, want)
	}
}

func TestAChoiceWithAnimationOffIsNotTold(t *testing.T) {
	a, _, heard := listening(false)

	choose(a, "Assistant 2")
	a.Slot()

	if len(*heard) != 0 {
		t.Fatalf("told %v with animation off", *heard)
	}
}

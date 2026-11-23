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

	entities := a.Controls().Entities()
	if len(entities) != 1 {
		t.Fatalf("%d entities, want the one switch", len(entities))
	}
	sw, ok := entities[0].(*esphome.Switch)
	if !ok || sw.ObjectID != "assistants_animate_announcements" || !sw.Get() {
		t.Fatalf("entity %+v, want the switch, on", entities[0])
	}

	sw.OnCommand(false)
	if *saved || a.Animated() || a.On() {
		t.Fatal("switching off did not save")
	}
	if err := a.Set(true); err != nil || !*saved {
		t.Fatalf("Set(true): saved %v, err %v", *saved, err)
	}
}

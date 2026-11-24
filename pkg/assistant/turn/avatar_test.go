package turn

import (
	"context"
	"slices"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type fakeAvatar struct {
	wants   map[int]bool
	handled bool
	spoke   []string
	slots   []int
	held    map[int]int
	woke    []int
}

func (a *fakeAvatar) Wants(slot int) bool { return a.wants[slot] }
func (a *fakeAvatar) Wake(slot int) {
	a.held[slot]++
	a.woke = append(a.woke, slot)
}
func (a *fakeAvatar) Rest(slot int) { a.held[slot]-- }
func (a *fakeAvatar) Speak(
	_ context.Context,
	slot int,
	url string,
	_ VoiceSink,
	started func(),
) (bool, error) {
	a.spoke, a.slots = append(a.spoke, url), append(a.slots, slot)
	if a.handled && started != nil {
		started()
	}
	return a.handled, nil
}

func withAvatar(a *announcing, wants ...int) *fakeAvatar {
	av := &fakeAvatar{wants: map[int]bool{}, handled: true, held: map[int]int{}}
	for _, s := range wants {
		av.wants[s] = true
	}
	a.options.Avatar = av
	return av
}

func TestAnAnimatedAnnouncementIsSpokenByTheAvatarAfterALocalChime(t *testing.T) {
	a := announcer(t)
	av := withAvatar(a, 1)
	a.choice.slot = 1

	a.hears(esphome.Announce{PreannounceMediaID: "chime", MediaID: "speech", Text: "Dinner"})
	a.plays()

	if !slices.Equal(a.fetched, []string{"chime"}) {
		t.Fatalf("played %v on the speaker, want only the chime", a.fetched)
	}
	if !slices.Equal(av.spoke, []string{"speech"}) || !slices.Equal(av.slots, []int{1}) {
		t.Fatalf("avatar spoke %v as %v", av.spoke, av.slots)
	}
	if got := a.last(t); got.Phase != Replying || got.Slot != 1 || got.Reply != "Dinner" {
		t.Fatalf("shown %+v, want assistant 2 replying", got)
	}
	a.ends()
	if av.held[1] != 0 || !slices.Equal(av.woke, []int{1}) {
		t.Fatalf("holds %v after one wake of %v", av.held, av.woke)
	}
	if !slices.Equal(a.reports, []bool{true}) {
		t.Fatalf("reports %v", a.reports)
	}
}

func TestAnAnnouncementTheAvatarCannotSpeakPlaysOnTheSpeaker(t *testing.T) {
	a := announcer(t)
	av := withAvatar(a, 0)
	av.handled = false

	a.hears(esphome.Announce{MediaID: "speech", Text: "Dinner"})
	a.plays()

	if !slices.Equal(av.spoke, []string{"speech"}) || !slices.Equal(a.fetched, []string{"speech"}) {
		t.Fatalf("avatar %v, speaker %v, want the avatar tried then the speaker", av.spoke, a.fetched)
	}
}

func TestAnAnnouncementForAnAssistantWithoutAnAvatarPlaysOnTheSpeaker(t *testing.T) {
	a := announcer(t)
	av := withAvatar(a, 1)

	a.hears(esphome.Announce{MediaID: "speech", Text: "Dinner"})
	a.plays()

	if len(av.spoke) != 0 || !slices.Equal(a.fetched, []string{"speech"}) {
		t.Fatalf("avatar %v, speaker %v", av.spoke, a.fetched)
	}
}

func TestWithAnimationOffTheAvatarNeverSpeaksAnAnnouncement(t *testing.T) {
	a := announcer(t)
	av := withAvatar(a, 0)
	a.choice.animated = false

	a.hears(esphome.Announce{MediaID: "speech"})
	a.plays()

	if len(av.spoke) != 0 || len(av.woke) != 0 {
		t.Fatalf("avatar spoke %v, woke %v", av.spoke, av.woke)
	}
}

func TestAReplyInAvatarModeIsSpokenByTheAvatar(t *testing.T) {
	a := announcer(t)
	av := withAvatar(a, 1)
	a.slot = 1
	a.phase = phaseThinking
	a.arm(time.Hour)

	a.speak("reply.mp3")
	a.plays()

	if !slices.Equal(av.spoke, []string{"reply.mp3"}) || len(a.fetched) != 0 {
		t.Fatalf("avatar %v, speaker %v", av.spoke, a.fetched)
	}
	if a.deadline != nil {
		t.Error("the reply's started did not disarm the thinking timer")
	}
}

func TestAnAvatarTakesWholeClipsEvenWhenTheSlotStreams(t *testing.T) {
	a := announcer(t)
	words := a.options.Words.Settings.(*wakeSection)
	words.wake.Words[0].Delivery = schema.DeliveryStream
	if !a.streams(0) {
		t.Fatal("a streaming slot does not stream")
	}

	withAvatar(a, 0)

	if a.streams(0) {
		t.Fatal("a slot in avatar mode streams its reply")
	}
}

func TestTheWholeClipChoiceHoldsForTheTurn(t *testing.T) {
	a := announcer(t)
	words := a.options.Words.Settings.(*wakeSection)
	words.wake.Words[0].Delivery = schema.DeliveryStream
	av := withAvatar(a, 0)

	a.holdAvatar(0)
	av.wants[0] = false

	if a.streams(0) {
		t.Fatal("turning avatar mode off mid-turn switched the reply to a stream")
	}
	a.restAvatar()
	if !a.streams(0) {
		t.Fatal("the next turn does not stream")
	}
}

func TestATurnHoldsItsAvatarUntilItEnds(t *testing.T) {
	a := announcer(t)
	av := withAvatar(a)

	a.holdAvatar(1)
	a.holdAvatar(0)
	if av.held[1] != 0 || av.held[0] != 1 {
		t.Fatalf("holds %v, want only the newer turn's", av.held)
	}
	a.restAvatar()
	a.restAvatar()

	if av.held[0] != 0 || av.held[1] != 0 {
		t.Fatalf("holds %v after the turn", av.held)
	}
}

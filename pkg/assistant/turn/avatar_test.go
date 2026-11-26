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
	fed     map[int]bool
	// rest is what is left of a reply handed back.
	rest []int16
}

func (a *fakeAvatar) Wants(slot int) bool { return a.wants[slot] }
func (a *fakeAvatar) Fed(slot int) bool   { return a.fed[slot] }
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
) (bool, []int16, error) {
	a.spoke, a.slots = append(a.spoke, url), append(a.slots, slot)
	if a.handled && started != nil {
		started()
	}
	return a.handled, a.rest, nil
}

func withAvatar(a *announcing, wants ...int) *fakeAvatar {
	av := &fakeAvatar{
		wants:   map[int]bool{},
		handled: true,
		held:    map[int]int{},
		fed:     map[int]bool{},
	}
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
		t.Fatalf(
			"avatar %v, speaker %v, want the avatar tried then the speaker",
			av.spoke,
			a.fetched,
		)
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

type countingVoice struct {
	fakeVoice
	played []int
}

func (v *countingVoice) PlayVoice(pcm []int16) { v.played = append(v.played, len(pcm)) }

// A reply the avatar broke off is spoken on from what the avatar left, not fetched and started
// again.
func TestAReplyTheAvatarBrokeOffResumesOnTheSpeaker(t *testing.T) {
	a := announcer(t)
	av := withAvatar(a, 1)
	av.handled, av.rest = false, make([]int16, 16000/2)
	voice := &countingVoice{}
	a.speaker = voice
	a.fetch = func(_ context.Context, url string, _ int) ([]int16, error) {
		a.fetched = append(a.fetched, url)
		return make([]int16, 2*16000), nil
	}
	a.slot = 1
	a.phase = phaseThinking
	a.arm(time.Hour)

	a.speak("reply.mp3")
	a.plays()

	if len(voice.played) == 0 || voice.played[0] != 16000/2 {
		t.Fatalf("played %v samples, want the half second the avatar left", voice.played)
	}
	if len(a.fetched) != 0 {
		t.Errorf("fetched %v again", a.fetched)
	}
}

// An avatar that hands the whole reply back leaves the device to fetch it.
func TestAReplyHandedBackWholeIsFetched(t *testing.T) {
	a := announcer(t)
	av := withAvatar(a, 1)
	av.handled = false
	voice := &countingVoice{}
	a.speaker = voice
	a.fetch = func(_ context.Context, url string, _ int) ([]int16, error) {
		a.fetched = append(a.fetched, url)
		return make([]int16, 2*16000), nil
	}
	a.slot = 1
	a.phase = phaseThinking
	a.arm(time.Hour)

	a.speak("reply.mp3")
	a.plays()

	if len(voice.played) == 0 || voice.played[0] != 2*16000 {
		t.Fatalf("played %v samples, want the whole clip", voice.played)
	}
}

// fedTurn is a turn thinking on slot 1, whose avatar Home Assistant feeds when fed.
func fedTurn(t *testing.T, fed bool) (*announcing, *fakeAvatar) {
	t.Helper()
	a := announcer(t)
	av := withAvatar(a, 1)
	av.fed[1] = fed
	a.slot = 1
	a.phase = phaseThinking
	a.arm(time.Hour)
	a.holdAvatar(1)
	return a, av
}

// Home Assistant starts streaming a long reply before its text is complete, and ha-vam feeds it
// to the avatar from then on: the reply starts there, not seconds later at the clip.
func TestAFedReplyStartsWhenHomeAssistantStartsStreamingIt(t *testing.T) {
	a, av := fedTurn(t, true)

	a.handle(event{kind: evRunURL, url: "reply.flac"})
	a.handle(event{kind: evStreamOffered})
	a.handle(event{kind: evReplyURL, url: "reply.flac"})
	a.plays()

	if !slices.Equal(av.spoke, []string{"reply.flac"}) {
		t.Fatalf("avatar spoke %v, want the reply once, from the stream's start", av.spoke)
	}
	if len(a.sound.claims) != 1 {
		t.Errorf("%d replies claimed the speaker, want one", len(a.sound.claims))
	}
}

func TestAnAvatarNotFedWaitsForTheWholeClip(t *testing.T) {
	a, av := fedTurn(t, false)

	a.handle(event{kind: evRunURL, url: "reply.flac"})
	a.handle(event{kind: evStreamOffered})

	if a.phase != phaseThinking || len(av.spoke) != 0 {
		t.Fatalf("phase %v, avatar spoke %v, want it to wait for the clip", a.phase, av.spoke)
	}
}

func TestAFedReplyWithoutARunURLStartsAtTheClip(t *testing.T) {
	a, av := fedTurn(t, true)

	a.handle(event{kind: evStreamOffered})
	a.handle(event{kind: evReplyURL, url: "reply.flac"})
	a.plays()

	if !slices.Equal(av.spoke, []string{"reply.flac"}) {
		t.Fatalf("avatar spoke %v", av.spoke)
	}
}

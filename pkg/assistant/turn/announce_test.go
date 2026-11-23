package turn

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/audio/tone"
	"github.com/ygelfand/libcountertop/pkg/hook"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type fakeClaim struct {
	play    func(context.Context, Speaker) error
	done    chan struct{}
	err     error
	stopped bool
}

func (c *fakeClaim) Done() <-chan struct{} { return c.done }
func (c *fakeClaim) Err() error            { return c.err }
func (c *fakeClaim) Stopped() bool         { return c.stopped }
func (c *fakeClaim) Quiet() time.Time      { return time.Time{} }

func (c *fakeClaim) finished() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *fakeClaim) end(stopped bool) {
	if c.finished() {
		return
	}
	c.stopped = stopped
	close(c.done)
}

// fakeSound hands out claims that play only when a test says so.
type fakeSound struct {
	claims   []*fakeClaim
	silenced int
}

func (s *fakeSound) Claim(_ string, play func(context.Context, Speaker) error) Claim {
	if now := s.playing(); now != nil {
		now.end(true)
	}
	c := &fakeClaim{play: play, done: make(chan struct{})}
	s.claims = append(s.claims, c)
	return c
}

func (s *fakeSound) playing() *fakeClaim {
	if len(s.claims) == 0 || s.claims[len(s.claims)-1].finished() {
		return nil
	}
	return s.claims[len(s.claims)-1]
}

func (s *fakeSound) Busy() bool { return s.playing() != nil }
func (s *fakeSound) Duck(bool)  {}
func (s *fakeSound) Silence() {
	s.silenced++
	if now := s.playing(); now != nil {
		now.end(true)
	}
}

type fakeSounding struct{ on bool }

func (p *fakeSounding) Sounding(on bool) { p.on = on }

type fakeVoice struct{}

func (fakeVoice) PlayVoice([]int16) {}
func (fakeVoice) Queued() int       { return 0 }
func (fakeVoice) Splices() uint64   { return 0 }
func (fakeVoice) Underruns() uint64 { return 0 }
func (fakeVoice) Clipped() uint64   { return 0 }

type fakeMic struct{}

func (fakeMic) Listen(string) (<-chan []int16, func()) { return nil, func() {} }
func (fakeMic) Recent(time.Duration) []int16           { return nil }
func (fakeMic) Over(time.Duration) float64             { return 0 }
func (fakeMic) Floor() float64                         { return 0 }
func (fakeMic) Gain() float64                          { return 0 }
func (fakeMic) SetAdapting(bool)                       {}
func (fakeMic) Clipped() uint64                        { return 0 }

type fakeRecorder struct{}

func (fakeRecorder) Opens(string, int)      {}
func (fakeRecorder) Frame([]byte)           {}
func (fakeRecorder) Closes()                {}
func (fakeRecorder) Seconds(string) float64 { return 0 }

type fakeChimes struct{}

func (fakeChimes) Sound(schema.Stream, float64, tone.Sound) {}

type fakeEffects struct{ played []string }

func (e *fakeEffects) Begin()                {}
func (e *fakeEffects) End()                  {}
func (e *fakeEffects) Elsewhere()            {}
func (e *fakeEffects) Clear()                { e.played = append(e.played, "clear") }
func (e *fakeEffects) Play(n string, _ bool) { e.played = append(e.played, n) }

type wakeSection struct{ wake schema.Wake }

func (s *wakeSection) Read() schema.Wake                  { return s.wake }
func (s *wakeSection) Update(fn func(*schema.Wake)) error { fn(&s.wake); return nil }

type fakeAnnouncements struct {
	animated bool
	slot     int
	asked    int
}

func (a *fakeAnnouncements) Animated() bool { return a.animated }
func (a *fakeAnnouncements) Slot() int {
	a.asked++
	slot := a.slot
	a.slot = 0
	return slot
}

type announcing struct {
	*Conversation

	sound     *fakeSound
	player    *fakeSounding
	effects   *fakeEffects
	choice    *fakeAnnouncements
	shown     []Showing
	reports   []bool
	fetched   []string
	fetchErr  error
	spawned   []func()
	followed  []int
	cancelled int
}

// announcer is a conversation whose sound, reports and goroutines a test steps by hand.
func announcer(t *testing.T) *announcing {
	t.Helper()
	a := &announcing{
		sound:   &fakeSound{},
		player:  &fakeSounding{},
		effects: &fakeEffects{},
		choice:  &fakeAnnouncements{animated: true},
	}
	words := &wakeSection{wake: schema.Wake{Words: []schema.WakeWord{
		{ReplyingEffect: "One"},
		{ReplyingEffect: "Two"},
	}}}
	shown := &hook.Hook[Showing]{}
	shown.Listen(func(s Showing) { a.shown = append(a.shown, s) })

	a.Conversation = &Conversation{
		vs:      &esphome.VoiceSatellite{},
		sound:   a.sound,
		player:  a.player,
		speaker: fakeVoice{},
		events:  make(chan event, 32),
		stopped: make(chan struct{}),
		options: Dependencies{
			Source:        fakeMic{},
			Sound:         a.sound,
			Recorder:      fakeRecorder{},
			Words:         Words{Settings: words, Speaker: fakeChimes{}},
			Muted:         func() (bool, error) { return false, nil },
			Presentation:  &Presentation{Settings: words, Shown: shown, Effects: a.effects},
			Announcements: a.choice,
			Cancelled:     func() { a.cancelled++ },
			Failure:       func() {},
		},
	}
	a.report = func(ok bool) { a.reports = append(a.reports, ok) }
	a.spawn = func(_ string, fn func()) { a.spawned = append(a.spawned, fn) }
	a.begin = func(slot int) { a.followed = append(a.followed, slot) }
	a.fetch = func(_ context.Context, url string, _ int) ([]int16, error) {
		a.fetched = append(a.fetched, url)
		return []int16{1}, a.fetchErr
	}
	t.Cleanup(func() {
		for _, c := range a.sound.claims {
			c.end(true)
		}
	})
	return a
}

// pump handles everything the conversation has been sent, as its Run loop would.
func (a *announcing) pump() {
	for {
		select {
		case e := <-a.events:
			a.handle(e)
		default:
			return
		}
	}
}

// plays runs the newest claim's sound to its end, or to its failure.
func (a *announcing) plays() {
	c := a.sound.claims[len(a.sound.claims)-1]
	c.err = c.play(context.Background(), fakeVoice{})
	a.pump()
}

// ends lets the goroutines that waited for a claim run, after the claim is over.
func (a *announcing) ends() {
	if now := a.sound.playing(); now != nil {
		now.end(false)
	}
	waiting := a.spawned
	a.spawned = nil
	for _, fn := range waiting {
		fn()
	}
	a.pump()
}

func (a *announcing) hears(msg esphome.Announce) {
	a.announce(msg)
	a.pump()
}

func (a *announcing) last(t *testing.T) Showing {
	t.Helper()
	if len(a.shown) == 0 {
		t.Fatal("nothing was shown")
	}
	return a.shown[len(a.shown)-1]
}

func TestAnAnnouncementShowsAsAReplyFromItsSpeech(t *testing.T) {
	a := announcer(t)

	a.hears(esphome.Announce{PreannounceMediaID: "chime", MediaID: "speech", Text: "Dinner"})
	if len(a.shown) != 0 || !a.player.on {
		t.Fatalf("before the speech: shown %+v, sounding %v", a.shown, a.player.on)
	}

	a.plays()
	want := Showing{Phase: Replying, Slot: 0, Reply: "Dinner"}
	if len(a.shown) != 1 || a.shown[0] != want {
		t.Fatalf("shown %+v, want only %+v", a.shown, want)
	}
	if !slices.Equal(a.effects.played, []string{"One"}) {
		t.Fatalf("effects %v, want the first assistant's replying effect", a.effects.played)
	}
	if !slices.Equal(a.fetched, []string{"chime", "speech"}) {
		t.Fatalf("fetched %v", a.fetched)
	}

	a.ends()
	if got := a.last(t); got.Phase != Idle {
		t.Fatalf("after the announcement: shown %+v, want idle", got)
	}
	if a.effects.played[len(a.effects.played)-1] != "clear" || a.player.on {
		t.Fatalf("effects %v, sounding %v", a.effects.played, a.player.on)
	}
	if !slices.Equal(a.reports, []bool{true}) {
		t.Fatalf("reports %v, want one success", a.reports)
	}
}

func TestAPreannounceChimeAloneIsNotShown(t *testing.T) {
	a := announcer(t)

	a.hears(esphome.Announce{PreannounceMediaID: "chime"})
	a.plays()
	a.ends()

	if len(a.shown) != 0 || len(a.effects.played) != 0 {
		t.Fatalf("shown %+v, effects %v, want nothing", a.shown, a.effects.played)
	}
	if !slices.Equal(a.reports, []bool{true}) {
		t.Fatalf("reports %v, want one success", a.reports)
	}
}

func TestAnAnnouncementThatFailsIsReportedAndNotShown(t *testing.T) {
	a := announcer(t)
	a.fetchErr = errors.New("unreachable")

	a.hears(esphome.Announce{MediaID: "speech", Text: "Dinner"})
	a.plays()
	a.ends()

	if len(a.shown) != 0 || a.player.on {
		t.Fatalf("shown %+v, sounding %v", a.shown, a.player.on)
	}
	if !slices.Equal(a.reports, []bool{false}) {
		t.Fatalf("reports %v, want one failure", a.reports)
	}
}

func TestStartConversationListensOnTheFirstAssistantAfterTheAnnouncement(t *testing.T) {
	a := announcer(t)
	a.choice.slot = 1

	a.hears(esphome.Announce{MediaID: "speech", Text: "Ready?", StartConversation: true})
	a.plays()
	if got := a.last(t); got.Phase != Replying || got.Slot != 1 {
		t.Fatalf("during the announcement: shown %+v, want replying as assistant 2", got)
	}
	if len(a.followed) != 0 {
		t.Fatal("listening began before the announcement ended")
	}

	a.ends()
	if !slices.Equal(a.followed, []int{0}) {
		t.Fatalf("listened on %v, want the first assistant once", a.followed)
	}
	if !slices.Equal(a.reports, []bool{true}) {
		t.Fatalf("reports %v", a.reports)
	}
}

func TestStopEndsAnAnnouncementAndStillReportsIt(t *testing.T) {
	a := announcer(t)

	a.hears(esphome.Announce{MediaID: "speech", Text: "Dinner", StartConversation: true})
	a.plays()
	a.handle(event{kind: evCancel})

	if a.sound.silenced != 1 || a.cancelled != 1 {
		t.Fatalf("silenced %d, cancelled %d", a.sound.silenced, a.cancelled)
	}
	if got := a.last(t); got.Phase != Idle || a.player.on {
		t.Fatalf("after stop: shown %+v, sounding %v", got, a.player.on)
	}

	a.ends()
	if !slices.Equal(a.reports, []bool{false}) {
		t.Fatalf("reports %v, want one failure", a.reports)
	}
	if len(a.followed) != 0 {
		t.Fatal("a stopped announcement went on to listen")
	}
	if got := a.last(t); got.Phase != Idle {
		t.Fatalf("a late event showed %+v", got)
	}
}

func TestAWakeWordInterruptsAnAnnouncement(t *testing.T) {
	a := announcer(t)

	a.hears(esphome.Announce{MediaID: "speech", Text: "Dinner"})
	a.plays()
	a.handle(event{kind: evStart, slot: 1})

	if a.sound.silenced != 1 {
		t.Fatalf("silenced %d times, want once", a.sound.silenced)
	}
	if got := a.last(t); got.Phase != Idle || a.player.on {
		t.Fatalf("after the wake word: shown %+v, sounding %v", got, a.player.on)
	}

	a.ends()
	if !slices.Equal(a.reports, []bool{false}) {
		t.Fatalf("reports %v, want one failure", a.reports)
	}
}

func TestAnAnnouncementWaitsForTheTurnToEnd(t *testing.T) {
	a := announcer(t)
	a.phase = phaseThinking

	a.hears(esphome.Announce{MediaID: "first", Text: "First"})
	a.hears(esphome.Announce{MediaID: "second", Text: "Second"})
	if len(a.sound.claims) != 0 || len(a.shown) != 0 {
		t.Fatalf("during the turn: %d claims, shown %+v", len(a.sound.claims), a.shown)
	}

	a.ends()
	if !slices.Equal(a.reports, []bool{false}) {
		t.Fatalf("reports %v, want the replaced announcement reported as failed", a.reports)
	}

	a.phase = phaseIdle
	a.handle(event{kind: evPending})
	a.plays()
	if got := a.last(t); got.Phase != Replying || got.Reply != "Second" {
		t.Fatalf("after the turn: shown %+v, want the second announcement", got)
	}

	a.ends()
	if !slices.Equal(a.reports, []bool{false, true}) {
		t.Fatalf("reports %v", a.reports)
	}
}

func TestAnAnnouncementStillWaitingWhenTheConversationEndsIsReported(t *testing.T) {
	a := announcer(t)
	a.phase = phaseThinking
	a.hears(esphome.Announce{MediaID: "speech"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.Run(ctx)
	a.ends()

	if !slices.Equal(a.reports, []bool{false}) {
		t.Fatalf("reports %v, want one failure", a.reports)
	}
}

func TestTheNamedAssistantSpeaksOneAnnouncement(t *testing.T) {
	a := announcer(t)
	a.choice.slot = 1

	for _, want := range []struct {
		slot   int
		effect string
	}{{1, "Two"}, {0, "One"}} {
		a.effects.played = nil
		a.hears(esphome.Announce{MediaID: "speech", Text: "Dinner"})
		a.plays()
		if got := a.last(t); got.Phase != Replying || got.Slot != want.slot {
			t.Fatalf("shown %+v, want replying as slot %d", got, want.slot)
		}
		if !slices.Equal(a.effects.played, []string{want.effect}) {
			t.Fatalf("effects %v, want %s", a.effects.played, want.effect)
		}
		a.ends()
	}
}

func TestASlotBelowTheFirstIsTheFirst(t *testing.T) {
	a := announcer(t)
	a.choice.slot = -3

	a.hears(esphome.Announce{MediaID: "speech"})
	a.plays()

	if got := a.last(t); got.Slot != 0 {
		t.Fatalf("shown %+v, want the first assistant", got)
	}
}

func TestWithAnimationOffAnAnnouncementPlaysAsItAlwaysDid(t *testing.T) {
	a := announcer(t)
	a.choice.animated = false
	a.choice.slot = 1

	a.announce(esphome.Announce{
		PreannounceMediaID: "chime", MediaID: "speech", Text: "Dinner", StartConversation: true,
	})
	if len(a.events) != 0 {
		t.Fatal("a plain announcement went through the conversation's events")
	}
	a.plays()
	a.ends()

	if len(a.shown) != 0 || len(a.effects.played) != 0 {
		t.Fatalf("shown %+v, effects %v, want nothing", a.shown, a.effects.played)
	}
	if !slices.Equal(a.reports, []bool{true}) || a.player.on {
		t.Fatalf("reports %v, sounding %v", a.reports, a.player.on)
	}
	if !slices.Equal(a.followed, []int{0}) {
		t.Fatalf("listened on %v, want the first assistant once", a.followed)
	}
	if a.choice.asked != 0 || a.choice.slot != 1 {
		t.Fatal("the named assistant was used up while animation is off")
	}
}

func TestWithAnimationOffStopStillReportsTheAnnouncement(t *testing.T) {
	a := announcer(t)
	a.choice.animated = false

	a.announce(esphome.Announce{MediaID: "speech", StartConversation: true})
	a.plays()
	a.handle(event{kind: evCancel})
	a.ends()

	if !slices.Equal(a.reports, []bool{false}) {
		t.Fatalf("reports %v, want one failure", a.reports)
	}
	if len(a.followed) != 0 {
		t.Fatal("a stopped announcement went on to listen")
	}
}

func TestSpeechFromAnotherPlayerShowsAsAReplyWithoutText(t *testing.T) {
	a := announcer(t)
	a.choice.slot = 1

	a.Spoken(true)
	a.pump()
	want := Showing{Phase: Replying, Slot: 1}
	if len(a.shown) != 1 || a.shown[0] != want {
		t.Fatalf("shown %+v, want only %+v", a.shown, want)
	}

	a.Spoken(false)
	a.pump()
	if got := a.last(t); got.Phase != Idle {
		t.Fatalf("after the speech: shown %+v, want idle", got)
	}
}

func TestSpeechFromAnotherPlayerIsNotShownDuringATurnOrWithAnimationOff(t *testing.T) {
	a := announcer(t)
	a.phase = phaseThinking
	a.Spoken(true)
	a.Spoken(false)
	a.pump()

	a.phase = phaseIdle
	a.choice.animated = false
	a.Spoken(true)
	a.Spoken(false)
	a.pump()

	if len(a.shown) != 0 || len(a.effects.played) != 0 {
		t.Fatalf("shown %+v, effects %v, want nothing", a.shown, a.effects.played)
	}
}

func TestAWakeWordInterruptsSpeechFromAnotherPlayer(t *testing.T) {
	a := announcer(t)
	a.Spoken(true)
	a.pump()

	a.handle(event{kind: evStart})

	if a.sound.silenced != 1 {
		t.Fatalf("silenced %d times, want once", a.sound.silenced)
	}
	if got := a.last(t); got.Phase != Idle {
		t.Fatalf("shown %+v, want idle", got)
	}
}

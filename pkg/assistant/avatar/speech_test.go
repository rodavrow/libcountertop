package avatar

import (
	"sync"
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/rtc"
)

type lane struct {
	mu      sync.Mutex
	voice   int
	native  int
	drained bool
	queued  int
}

func (l *lane) PlayVoice(pcm []int16) { l.mu.Lock(); l.voice += len(pcm); l.mu.Unlock() }
func (l *lane) Queued() int           { return l.queued }
func (l *lane) Drain()                { l.drained = true }

type wideLane struct{ lane }

func (l *wideLane) PlayVoiceAt(rate, channels int, pcm []int16) {
	if rate != rtc.PlayRate || channels != rtc.PlayChannels {
		panic("wrong format")
	}
	l.mu.Lock()
	l.native += len(pcm)
	l.mu.Unlock()
}

type clock struct{ at time.Time }

func (c *clock) now() time.Time       { return c.at }
func (c *clock) pass(d time.Duration) { c.at = c.at.Add(d) }
func chunk(level int16) []int16 {
	// 20 ms of 48 kHz stereo, as one Opus packet decodes to.
	out := make([]int16, rtc.PlayRate/50*rtc.PlayChannels)
	for i := range out {
		if i%2 == 0 {
			out[i] = level
		} else {
			out[i] = -level
		}
	}
	return out
}

func TestSilenceBeforeTheReplyIsNotPlayed(t *testing.T) {
	l, c := &lane{}, &clock{at: time.Unix(0, 0)}
	started := 0
	s := newSpeech(l, func() { started++ }, c.now)

	for range 50 {
		s.hear(chunk(0))
		c.pass(20 * time.Millisecond)
	}

	if l.voice != 0 || started != 0 {
		t.Fatalf("played %d samples, started %d times on silence", l.voice, started)
	}
}

func TestTheFirstAudibleChunkStartsTheReplyAndIsHeldForJitter(t *testing.T) {
	l, c := &lane{}, &clock{at: time.Unix(0, 0)}
	started := 0
	s := newSpeech(l, func() { started++ }, c.now)

	s.hear(chunk(4000))
	if started != 0 || l.voice != 0 {
		t.Fatalf("started %d times, played %d samples before the jitter buffer filled", started, l.voice)
	}
	if first, _ := s.heard(); first.IsZero() {
		t.Fatal("the first audible chunk was not noticed")
	}
	for range int(jitter / (20 * time.Millisecond)) {
		s.hear(chunk(4000))
	}
	if l.voice == 0 || started != 1 {
		t.Fatalf("played %d samples, started %d times once the jitter buffer filled", l.voice, started)
	}
	s.hear(chunk(0))
	if started != 1 {
		t.Fatalf("started %d times", started)
	}
}

func TestANarrowLaneGetsSixteenKilohertzMono(t *testing.T) {
	l, c := &lane{}, &clock{at: time.Unix(0, 0)}
	s := newSpeech(l, nil, c.now)
	chunks := 50
	for range chunks {
		s.hear(chunk(4000))
	}

	want := chunks * voiceRate / 50
	if l.voice < want*9/10 || l.voice > want {
		t.Fatalf("played %d samples for %d ms, want about %d", l.voice, chunks*20, want)
	}
}

func TestANativeLaneGetsTheStreamAsIs(t *testing.T) {
	l, c := &wideLane{}, &clock{at: time.Unix(0, 0)}
	s := newSpeech(l, nil, c.now)
	for range 20 {
		s.hear(chunk(4000))
	}

	if l.native != 20*len(chunk(0)) || l.voice != 0 {
		t.Fatalf("native %d, narrow %d samples", l.native, l.voice)
	}
}

func TestHeardTracksTheFirstAndLastAudibleAudio(t *testing.T) {
	l, c := &lane{}, &clock{at: time.Unix(10, 0)}
	s := newSpeech(l, nil, c.now)
	s.hear(chunk(4000))
	first := c.at
	c.pass(time.Second)
	s.hear(chunk(4000))
	last := c.at
	c.pass(time.Second)
	s.hear(chunk(10))

	gotFirst, gotLast := s.heard()
	if !gotFirst.Equal(first) || !gotLast.Equal(last) {
		t.Fatalf("heard %v..%v, want %v..%v", gotFirst, gotLast, first, last)
	}
}

func TestStoppingDropsWhatIsHeldAndCanDrainTheLane(t *testing.T) {
	l, c := &lane{}, &clock{at: time.Unix(0, 0)}
	s := newSpeech(l, nil, c.now)
	s.hear(chunk(4000))

	s.stop(true)
	for range 20 {
		s.hear(chunk(4000))
	}

	if l.voice != 0 || !l.drained {
		t.Fatalf("played %d samples after stop, drained %v", l.voice, l.drained)
	}
}

func TestTheVideoDelayOnlyGrowsWithinAReply(t *testing.T) {
	s := newSpeech(&lane{}, nil, time.Now)

	for _, c := range []struct {
		want, got time.Duration
		grew      bool
	}{
		{200 * time.Millisecond, 200 * time.Millisecond, true},
		{150 * time.Millisecond, 200 * time.Millisecond, false},
		{300 * time.Millisecond, 300 * time.Millisecond, true},
	} {
		if got, grew := s.delay(c.want); got != c.got || grew != c.grew {
			t.Errorf("asked %v: %v (grew %v), want %v (grew %v)", c.want, got, grew, c.got, c.grew)
		}
	}
}

func TestLatencyCountsTheJitterBufferAndTheQueue(t *testing.T) {
	l := &lane{queued: voiceRate / 2}
	s := newSpeech(l, nil, time.Now)

	if got, want := s.latency(), jitter+500*time.Millisecond; got != want {
		t.Fatalf("latency %v, want %v", got, want)
	}
}

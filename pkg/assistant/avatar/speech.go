package avatar

import (
	"sync"
	"time"

	"github.com/ygelfand/libcountertop/pkg/assistant/turn"
	"github.com/ygelfand/libcountertop/pkg/audio/resample"
	"github.com/ygelfand/libcountertop/pkg/media/rtc"
)

const (
	// audible is the peak above which a chunk counts as speech; LiveTalking's idle stream is
	// digital silence, and its speech starts well above this.
	audible = 300

	// jitter is collected after the first audible chunk before anything is played, so network
	// jitter does not click.
	jitter = 150 * time.Millisecond

	voiceRate = 16000
)

// Sink is the speaker's voice lane, as the conversation plays replies into it.
type Sink = turn.VoiceSink

// TimedSink is a voice lane that says how long its queue takes to play. Without it Queued is
// taken as 16 kHz samples, which a lane counting at its own rate gets wrong.
type TimedSink interface {
	QueuedFor() time.Duration
}

// NativeSink is a voice lane that takes PCM at another rate and channel count, so the
// avatar's 48 kHz stereo is not narrowed to 16 kHz mono and back.
type NativeSink interface {
	PlayVoiceAt(rate, channels int, pcm []int16)
}

// Drainer is a voice lane that can drop what it has queued.
type Drainer interface{ Drain() }

// speech carries one reply's stream audio into the voice lane.
type speech struct {
	sink   Sink
	native NativeSink
	narrow *resample.Rational
	now    func() time.Time

	mu      sync.Mutex
	held    [][]int16
	heldFor time.Duration
	flowing bool
	stopped bool
	firstAt time.Time
	loudAt  time.Time
	started func()
	delayed time.Duration
	// handed is how much of the reply went to the voice lane; flowingAt is when it started to.
	handed    time.Duration
	flowingAt time.Time
}

func newSpeech(sink Sink, started func(), now func() time.Time) *speech {
	s := &speech{sink: sink, now: now, started: started}
	if n, ok := sink.(NativeSink); ok {
		s.native = n
	} else {
		s.narrow = resample.NewRational(rtc.PlayRate, voiceRate, 1)
	}
	return s
}

// hear takes one decoded chunk of 48 kHz stereo from the stream.
func (s *speech) hear(pcm []int16) {
	loud := peak(pcm) >= audible
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	now := s.now()
	if loud {
		s.loudAt = now
	}
	if s.firstAt.IsZero() {
		if !loud {
			return
		}
		s.firstAt = now
	}
	if s.flowing {
		s.play(pcm)
		return
	}
	s.held = append(s.held, pcm)
	s.heldFor += time.Duration(len(pcm)/rtc.PlayChannels) * time.Second / rtc.PlayRate
	if s.heldFor < jitter {
		return
	}
	s.flowing, s.flowingAt = true, now
	// The reply starts when its sound does, after the jitter buffer, so captions match it.
	if s.started != nil {
		s.started()
	}
	for _, h := range s.held {
		s.play(h)
	}
	s.held = nil
}

func (s *speech) play(pcm []int16) {
	s.handed += time.Duration(len(pcm)/rtc.PlayChannels) * time.Second / rtc.PlayRate
	if s.native != nil {
		s.native.PlayVoiceAt(rtc.PlayRate, rtc.PlayChannels, pcm)
		return
	}
	s.sink.PlayVoice(s.narrow.Run(downmix(pcm)))
}

// stop ends the reply; anything still held is dropped and, when asked, so is the queue.
func (s *speech) stop(drain bool) {
	s.mu.Lock()
	s.stopped, s.held = true, nil
	s.mu.Unlock()
	if d, ok := s.sink.(Drainer); ok && drain {
		d.Drain()
	}
}

// spoken is how far into the reply the speaker has played: what went to the voice lane, less
// what still waits in its queue.
func (s *speech) spoken() time.Duration {
	s.mu.Lock()
	handed := s.handed
	s.mu.Unlock()
	return max(handed-(s.latency()-jitter), 0)
}

// voiced is when audible audio first arrived, when it started to reach the voice lane, and how
// much of it did: zero times for a reply that stayed silent.
func (s *speech) voiced() (first, flowing time.Time, handed time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstAt, s.flowingAt, s.handed
}

// heard reports when audible audio first and last arrived; zero when it has not.
func (s *speech) heard() (first, last time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstAt, s.loudAt
}

// latency is how long a chunk arriving now waits before it is heard, not counting the board's
// own output delay.
func (s *speech) latency() time.Duration {
	if t, ok := s.sink.(TimedSink); ok {
		return jitter + t.QueuedFor()
	}
	queued := time.Duration(s.sink.Queued()) * time.Second / voiceRate
	return jitter + queued
}

// delay keeps the largest video delay asked for in this reply and reports whether it grew.
func (s *speech) delay(want time.Duration) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if want <= s.delayed {
		return s.delayed, false
	}
	s.delayed = want
	return want, true
}

func downmix(pcm []int16) []int16 {
	out := make([]int16, len(pcm)/rtc.PlayChannels)
	for i := range out {
		var sum int
		for c := range rtc.PlayChannels {
			sum += int(pcm[i*rtc.PlayChannels+c])
		}
		out[i] = int16(sum / rtc.PlayChannels)
	}
	return out
}

func peak(pcm []int16) int {
	top := 0
	for _, v := range pcm {
		top = max(top, int(v), -int(v))
	}
	return top
}

package turn

import (
	"context"
	"time"

	"github.com/ygelfand/libcountertop/pkg/assistant/activity"
	"github.com/ygelfand/libcountertop/pkg/inference/wake"
)

type Microphone interface {
	Listen(string) (<-chan []int16, func())
	Recent(time.Duration) []int16
	Over(time.Duration) float64
	Floor() float64
	Gain() float64
	SetAdapting(bool)
	Clipped() uint64
}

type Speaker interface {
	VoiceSink
	Splices() uint64
	Underruns() uint64
	Clipped() uint64
}

type Claim interface {
	Done() <-chan struct{}
	Err() error
	Stopped() bool
	Quiet() time.Time
}

type Sound interface {
	Claim(string, func(context.Context, Speaker) error) Claim
	Busy() bool
	Silence()
	Duck(bool)
}

type Arbiter interface {
	Decide(word string, score float64) bool
}

type Voices interface {
	Spoke(at time.Time, told func(name, person string, score float64, err error)) bool
	Claim(at time.Time) bool
	Wake(phrase func() string)
}

type Recorder interface {
	Opens(string, int)
	Frame([]byte)
	Closes()
	Seconds(string) float64
}

// Announcements says whether speech Home Assistant pushes is shown as a reply, and as whose.
type Announcements interface {
	Animated() bool

	// Slot is the assistant the next announcement speaks as. Asking uses the choice up.
	Slot() int
}

// Avatar speaks a wake word's replies through its LiveTalking avatar instead of the speaker.
type Avatar interface {
	// Wants reports whether slot's replies go to its avatar, which needs whole clips.
	Wants(slot int) bool

	// Wake holds slot's avatar session open for a turn or announcement; Rest lets it go.
	Wake(slot int)
	Rest(slot int)

	// Fed reports whether Home Assistant streams slot's replies into its avatar session, so the
	// reply starts as soon as Home Assistant starts streaming it.
	Fed(slot int) bool

	// Speak has slot's avatar say the clip at url, its voice going into sink. handled is false
	// when the caller should play the clip itself, from from on.
	Speak(
		ctx context.Context, slot int, url string, sink VoiceSink, started func(),
	) (handled bool, from time.Duration, err error)
}

type Dependencies struct {
	Source             Microphone
	Speaker            Speaker
	Sound              Sound
	Player             interface{ Sounding(bool) }
	Log                *activity.Log
	Recorder           Recorder
	Words              Words
	Word               func(int) (string, bool)
	Models             func() []wake.Model
	Muted              func() (bool, error)
	HardwareTail       time.Duration
	Presentation       *Presentation
	Arbiter            Arbiter
	Voices             Voices
	Announcements      Announcements
	Avatar             Avatar
	Cancelled, Failure func()
}

func dropped(source Microphone) uint64 {
	if s, ok := source.(interface{ Dropped() uint64 }); ok {
		return s.Dropped()
	}
	return 0
}

package assistant

import (
	"testing"
	"time"

	voice "github.com/ygelfand/libcountertop/pkg/assistant/turn"
)

type quiet struct{}

func (quiet) Level() float64 { return 0 }

func TestTheSpeakerBelongsToTheTurnItWokeAndNoOther(t *testing.T) {
	a := New(Dependencies{Microphone: quiet{}})
	woke := time.Now()
	a.Show(voice.Showing{Phase: voice.Listening})
	a.Spoke(Speaker{ID: "person.alex", Name: "Alex"}, woke)
	if s, _ := a.Now(); s.Speaker.Name != "Alex" {
		t.Errorf("speaker %+v, want Alex for the turn that just opened", s.Speaker)
	}
	a.Spoke(Speaker{ID: "person.beth", Name: "Beth"}, woke.Add(-time.Minute))
	if s, _ := a.Now(); s.Speaker.Name != "Alex" {
		t.Errorf("a match for an old wake replaced the speaker: %+v", s.Speaker)
	}
	a.Show(voice.Showing{Phase: voice.Idle})
	a.Show(voice.Showing{Phase: voice.Listening})
	if s, _ := a.Now(); s.Speaker.Name != "" {
		t.Errorf("a new turn kept the old speaker %+v", s.Speaker)
	}
}

package urlplayer

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/cast"
)

type finishingRemote struct{ finished atomic.Int32 }

func (r *finishingRemote) Play() error              { return nil }
func (r *finishingRemote) Pause() error             { return nil }
func (r *finishingRemote) Stop() error              { return nil }
func (r *finishingRemote) Seek(time.Duration) error { return nil }
func (r *finishingRemote) Failed(error)             {}
func (r *finishingRemote) Finished()                { r.finished.Add(1) }

func TestTheEndOfTheCurrentTrackReachesTheRemote(t *testing.T) {
	url := serve(t, "audio/wav", wavFile(outRate, ramp(outRate)), true)
	out := &fakeOutput{}
	r := &finishingRemote{}
	p := New(Options{Output: out, Remote: r})
	if err := p.Load(cast.Media{ContentID: url}, 0, true); err != nil {
		t.Fatal(err)
	}
	out.wait(t, "play")
	out.last().Finished()
	if got := r.finished.Load(); got != 1 {
		t.Fatalf("the remote heard the end %d times, want 1", got)
	}
}

func TestTheEndOfAReplacedTrackIsNotPassedOn(t *testing.T) {
	url := serve(t, "audio/wav", wavFile(outRate, ramp(outRate)), true)
	out := &fakeOutput{}
	r := &finishingRemote{}
	p := New(Options{Output: out, Remote: r})
	if err := p.Load(cast.Media{ContentID: url}, 0, true); err != nil {
		t.Fatal(err)
	}
	out.wait(t, "play")
	old := out.last()
	if err := p.Seek(400 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	out.wait(t, "play handoff play")
	old.Finished()
	if got := r.finished.Load(); got != 0 {
		t.Fatalf("the remote heard the end of a replaced track %d times", got)
	}
	_ = p.Stop()
}

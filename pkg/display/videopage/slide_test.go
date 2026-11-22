package videopage

import (
	"sync"
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/media/nowplaying"
)

type short struct {
	fake
	mu   sync.Mutex
	next int
}

func (s *short) Now() nowplaying.Now {
	return nowplaying.Now{
		Playing: true,
		Short:   true,
		Can:     nowplaying.CanNext | nowplaying.CanPrevious,
	}
}

func (s *short) Next() {
	s.mu.Lock()
	s.next++
	s.mu.Unlock()
}

func (s *short) skipped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next
}

type track struct {
	mu   sync.Mutex
	last int
	seen int
}

func (t *track) move(dy int) {
	t.mu.Lock()
	t.last, t.seen = dy, t.seen+1
	t.mu.Unlock()
}

func (t *track) at() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last
}

func settle(t *testing.T, want func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !want() {
		if time.Now().After(deadline) {
			t.Fatal("the slide never settled")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func slidingPage(t *testing.T, c Controls) (*Page, *track) {
	t.Helper()
	stack := shell.New()
	t.Cleanup(stack.Close)
	p := New(c, Dependencies{Shell: stack})
	tr := &track{}
	p.SlideWith(tr.move)
	return p, tr
}

func TestALongDragOnAShortSlidesOffAndTheNextSlidesIn(t *testing.T) {
	c := &short{}
	p, tr := slidingPage(t, c)
	p.DragStart(500)
	p.DragTo(200, 800)
	if tr.at() != -300 {
		t.Fatalf("the picture sits at %d while dragged 300 up", tr.at())
	}
	p.DragEnd(0, 800)
	settle(t, func() bool { return c.skipped() == 1 })
	if tr.at() != -800 {
		t.Errorf("the picture left to %d, want off the top at -800", tr.at())
	}
	p.SetPicture(true)
	settle(t, func() bool { return tr.at() == 0 })
}

func TestAShortDragSnapsBack(t *testing.T) {
	c := &short{}
	p, tr := slidingPage(t, c)
	p.DragStart(500)
	p.DragTo(450, 800)
	p.DragEnd(0, 800)
	settle(t, func() bool { return tr.at() == 0 })
	if c.skipped() != 0 {
		t.Error("a small drag skipped the short")
	}
}

func TestOnlyShortsSlide(t *testing.T) {
	p, _ := slidingPage(t, playing())
	if p.Slides() {
		t.Error("a regular video slides")
	}
}

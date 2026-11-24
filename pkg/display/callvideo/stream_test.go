package callvideo

import (
	"testing"

	display "github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/display/surface"
)

type noHelper struct{}

func (noHelper) Helper() *surface.Client          { return nil }
func (noHelper) Orientation() display.Orientation { return display.Orientation(0) }
func (noHelper) Native() (int, int)               { return 1280, 800 }

func TestAPlacedStreamFitsInsideItsBox(t *testing.T) {
	s := NewStream(noHelper{}, surface.AVC, 940, -3)
	s.l.size = Size{Width: 512, Height: 512}
	box := display.Rect{X: 100, Y: 50, W: 400, H: 300}

	s.Place(box, 6)

	got, ok := s.Fitted(1280, 800)
	if !ok || !inside(got, box) || got.H != box.H {
		t.Fatalf("fitted %+v (ok %v), want the height of %+v", got, ok, box)
	}
}

func TestHidingAStreamKeepsItsPlace(t *testing.T) {
	s := NewStream(noHelper{}, surface.AVC, 940, -3)
	s.l.size = Size{Width: 512, Height: 512}
	box := display.Rect{X: 10, Y: 10, W: 200, H: 200}
	s.Place(box, -3)

	s.Hide()

	if !s.l.hidden {
		t.Error("the stream is not hidden")
	}
	if got, _ := s.Fitted(1280, 800); got != box {
		t.Errorf("hidden stream moved to %+v", got)
	}
	s.Place(box, 6)
	if s.l.hidden || s.l.z != 6 {
		t.Error("placing the stream again leaves it hidden")
	}
}

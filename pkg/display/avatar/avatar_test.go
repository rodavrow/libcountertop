package avatar

import (
	"context"
	"errors"
	"image"
	"image/color"
	"sync/atomic"
	"testing"
	"time"
)

type source struct {
	people  []Person
	fetched atomic.Int32
}

func (s *source) People(context.Context) ([]Person, error) { return s.people, nil }

func (s *source) Picture(context.Context, string) (image.Image, error) {
	s.fetched.Add(1)
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			img.Set(x, y, color.RGBA{0x80, 0x40, 0x20, 0xff})
		}
	}
	return img, nil
}

func TestInitialsTakeTheFirstTwoWords(t *testing.T) {
	for name, want := range map[string]string{
		"Alex":           "A",
		"Mary Ann Smith": "MA",
		"  beth ":        "B",
		"":               "",
	} {
		if got := Initials(name); got != want {
			t.Errorf("Initials(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestAPictureIsFetchedOnceUntilItsPathChanges(t *testing.T) {
	src := &source{people: []Person{{ID: "person.alex", Name: "Alex", Picture: "/a"}}}
	done := make(chan struct{}, 4)
	b := NewBook(src, func() { done <- struct{}{} })
	for range 2 {
		b.Refresh()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the people list never loaded")
		}
	}
	if n := src.fetched.Load(); n != 1 {
		t.Errorf("fetched %d times for an unchanged path, want 1", n)
	}
	src.people = []Person{{ID: "person.alex", Name: "Alex", Picture: "/b"}}
	b.Refresh()
	<-done
	if n := src.fetched.Load(); n != 2 {
		t.Errorf("fetched %d times after the path changed, want 2", n)
	}
}

type flaky struct {
	source
	fail atomic.Bool
}

func (f *flaky) Picture(ctx context.Context, path string) (image.Image, error) {
	if f.fail.Load() {
		return nil, errors.New("no address yet")
	}
	return f.source.Picture(ctx, path)
}

func TestAFailedPictureIsRetriedOnTheNextLook(t *testing.T) {
	src := &flaky{
		source: source{people: []Person{{ID: "person.alex", Name: "Alex", Picture: "/a"}}},
	}
	src.fail.Store(true)
	done := make(chan struct{}, 4)
	b := NewBook(src, func() { done <- struct{}{} })
	b.Refresh()
	<-done
	src.fail.Store(false)
	b.People()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("looking at the people again did not retry the picture")
	}
	if n := src.fetched.Load(); n != 1 {
		t.Errorf("fetched %d pictures after the retry, want 1", n)
	}
}

func TestAPictureIsCutToACircle(t *testing.T) {
	src := &source{}
	img, _ := src.Picture(context.Background(), "")
	r := round(img)
	if r.Bounds().Dx() != pixels || r.Bounds().Dy() != pixels {
		t.Fatalf("size %v, want %d square", r.Bounds(), pixels)
	}
	if a := r.NRGBAAt(0, 0).A; a != 0 {
		t.Errorf("corner alpha %d, want 0", a)
	}
	if a := r.NRGBAAt(pixels/2, pixels/2).A; a != 0xff {
		t.Errorf("center alpha %d, want opaque", a)
	}
}

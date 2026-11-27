package gogui

import (
	"context"
	"math"
	"os"

	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

type frame struct {
	quads []float32
	runs  []uint32
}

type sink struct {
	programs map[int]bool
	frames   []frame
}

func (s *sink) Program(slot int, vs, fs string) error {
	if s.programs == nil {
		s.programs = map[int]bool{}
	}
	s.programs[slot] = vs != "" && fs != ""
	return nil
}

func (s *sink) Texture(uint32, int, int, int, int, int, int, []byte) error { return nil }

func (s *sink) Frame(_ int, _ [4]float32, quads []float32, runs []uint32) (uint32, uint32, error) {
	s.frames = append(
		s.frames,
		frame{append([]float32(nil), quads...), append([]uint32(nil), runs...)},
	)
	return 0, 0, nil
}

type app struct{ on bool }

func view(w *gui.Window) gui.View {
	a := gui.State[app](w)
	return gui.Column(gui.ContainerCfg{
		Sizing: gui.FillFill,
		Content: []gui.View{
			gui.ProgressBar(gui.ProgressBarCfg{Percent: 0.4, Width: 200}),
			gui.Switch(gui.SwitchCfg{Selected: a.on}),
		},
	})
}

func TestAFrameIsOneWellFormedBatch(t *testing.T) {
	s := &sink{}
	w := gui.SimpleWindow("test", 320, 200, &app{on: true}, func(w *gui.Window) { w.SetView(view) })
	r, err := New(s, w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	if !s.programs[slotSolid] || !s.programs[slotGlyph] {
		t.Fatalf("programs %v", s.programs)
	}
	w.Config.OnInit(w)

	st, err := r.Render(w)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.frames) != 1 || st.Quads == 0 {
		t.Fatalf("%d frames, %d quads", len(s.frames), st.Quads)
	}
	f := s.frames[0]
	if len(f.quads)%quadFloats != 0 || len(f.runs)%runWords != 0 {
		t.Fatalf("ragged batch: %d floats, %d run words", len(f.quads), len(f.runs))
	}
	n := uint32(len(f.quads) / quadFloats)
	var covered uint32
	for i := 0; i < len(f.runs); i += runWords {
		first, count := f.runs[i+6], f.runs[i+7]
		if count == 0 || first != covered || first+count > n {
			t.Fatalf(
				"run %d covers %d+%d of %d quads (expected to start at %d)",
				i/runWords,
				first,
				count,
				n,
				covered,
			)
		}
		covered += count
	}
	if covered != n {
		t.Errorf("runs cover %d of %d quads", covered, n)
	}
}

func TestPackedParamsUnpackToRadiusAndThickness(t *testing.T) {
	p := packParams(12.25, 1.5)
	radius := float32(int(p/4096)) / 4
	thickness := float32(int(p)%4096) / 4
	if radius != 12.25 || thickness != 1.5 {
		t.Errorf("unpacked %v, %v", radius, thickness)
	}
}

type counter struct{ taps int }

type countingSink struct {
	sink
	frames chan int
}

func (s *countingSink) Frame(
	rot int,
	clear [4]float32,
	quads []float32,
	runs []uint32,
) (uint32, uint32, error) {
	s.frames <- len(quads) / quadFloats
	return 0, 0, nil
}

func TestATouchReachesTheViewAndDrawsAFrame(t *testing.T) {
	s := &countingSink{frames: make(chan int, 16)}
	taps := make(chan int, 4)
	w := gui.SimpleWindow("loop", 320, 200, &counter{}, func(w *gui.Window) {
		w.SetView(func(w *gui.Window) gui.View {
			return gui.Button(gui.ButtonCfg{
				ID:     "all",
				Label:  "",
				Sizing: gui.FillFill,
				OnClick: func(e gui.EventCtx) {
					c := gui.State[counter](e.Window)
					c.taps++
					taps <- c.taps
				},
			})
		})
	})
	r, err := New(s, w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, w) }()

	select {
	case <-s.frames:
	case <-time.After(2 * time.Second):
		t.Fatal("no first frame")
	}
	r.Touch(Began, 1, 160, 100)
	r.Touch(Ended, 1, 160, 100)
	select {
	case n := <-taps:
		if n != 1 {
			t.Errorf("%d taps", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the tap never reached the button")
	}
	cancel()
	r.poke()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

type textureSink struct {
	sink
	uploads []uint32
}

func (s *textureSink) Texture(id uint32, _, _, _, _, _, _ int, _ []byte) error {
	s.uploads = append(s.uploads, id)
	return nil
}

func TestAMemoryImageUploadsOnceWhileItStaysRegistered(t *testing.T) {
	pix := make([]byte, 8*8*4)
	first := gui.UseImage("gogui-test/a", 8, 8, pix)
	second := gui.UseImage("gogui-test/b", 8, 8, pix)
	src := first
	r, s, w := memRenderer(t, &src)
	render := func() {
		if _, err := r.Render(w); err != nil {
			t.Fatal(err)
		}
	}
	base := len(s.uploads)
	for range 3 {
		render()
	}
	src = second
	render()
	src = first
	render()
	if got := s.uploads[base:]; len(got) != 2 {
		t.Errorf("uploads %v, want one for each image", got)
	}
}

func TestADroppedMemoryImageFreesItsSlot(t *testing.T) {
	pix := make([]byte, 8*8*4)
	first := gui.UseImage("gogui-test/drop-a", 8, 8, pix)
	src := first
	r, s, w := memRenderer(t, &src)
	if _, err := r.Render(w); err != nil {
		t.Fatal(err)
	}
	slot := s.uploads[len(s.uploads)-1]
	gui.DropImage("gogui-test/drop-a")
	if _, err := r.Render(w); err != nil {
		t.Fatal(err)
	}
	src = gui.UseImage("gogui-test/drop-b", 8, 8, pix)
	if _, err := r.Render(w); err != nil {
		t.Fatal(err)
	}
	if got := s.uploads[len(s.uploads)-1]; got != slot {
		t.Errorf("the new image went to slot %d, want the dropped image's %d", got, slot)
	}
}

func TestAKeyRegisteredAgainAtANewSizeUploadsAgain(t *testing.T) {
	src := gui.UseImage("gogui-test/resize", 8, 8, make([]byte, 8*8*4))
	r, s, w := memRenderer(t, &src)
	if _, err := r.Render(w); err != nil {
		t.Fatal(err)
	}
	base := len(s.uploads)
	gui.UseImage("gogui-test/resize", 16, 16, make([]byte, 16*16*4))
	if _, err := r.Render(w); err != nil {
		t.Fatal(err)
	}
	if got := s.uploads[base:]; len(got) != 1 {
		t.Errorf("%d uploads after the key changed size, want 1", len(got))
	}
}

func memRenderer(t *testing.T, src *string) (*Renderer, *textureSink, *gui.Window) {
	t.Helper()
	w := gui.SimpleWindow("mem", 64, 64, &counter{}, func(w *gui.Window) {
		w.SetView(func(*gui.Window) gui.View {
			return gui.Image(gui.ImageCfg{Src: *src, Width: 8, Height: 8})
		})
	})
	s := &textureSink{}
	r, err := New(s, w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	w.Config.OnInit(w)
	return r, s, w
}

func TestAStillViewStopsDrawing(t *testing.T) {
	s := &countingSink{frames: make(chan int, 1024)}
	w := gui.SimpleWindow("still", 320, 200, &counter{}, func(w *gui.Window) {
		w.SetView(func(*gui.Window) gui.View {
			return gui.Column(
				gui.ContainerCfg{
					Sizing:  gui.FillFill,
					Content: []gui.View{gui.Text(gui.TextCfg{Text: "12:00"})},
				},
			)
		})
	})
	r, err := New(s, w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx, w) }()
	time.Sleep(500 * time.Millisecond)
	if n := len(s.frames); n > 3 {
		t.Errorf("%d frames in half a second of a still view", n)
	}
}

func glyphBounds(t *testing.T, angle float32) (w, h float32) {
	t.Helper()
	s := &sink{}
	win := gui.SimpleWindow("turn", 400, 400, &app{}, func(w *gui.Window) {
		w.SetView(func(*gui.Window) gui.View {
			return gui.DrawCanvas(gui.DrawCanvasCfg{
				ID: "turn", Width: 400, Height: 400,
				OnDraw: func(dc *gui.DrawContext) {
					st := gui.CurrentTheme().Cfg.TextStyleDef
					st.Size, st.RotationRadians = 40, angle
					dc.Text(200, 200, "WWWWWW", st)
				},
			})
		})
	})
	r, err := New(s, win)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	win.Config.OnInit(win)
	if _, err := r.Render(win); err != nil {
		t.Fatal(err)
	}
	f := s.frames[len(s.frames)-1]
	minX, minY, maxX, maxY := float32(1e9), float32(1e9), float32(-1e9), float32(-1e9)
	for i := 0; i < len(f.runs); i += runWords {
		if f.runs[i] != slotGlyph {
			continue
		}
		first, count := int(f.runs[i+6]), int(f.runs[i+7])
		for q := first; q < first+count; q++ {
			for v := range 4 {
				x, y := f.quads[q*quadFloats+v*9], f.quads[q*quadFloats+v*9+1]
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
		}
	}
	if maxX < minX {
		t.Fatal("no glyphs drawn")
	}
	return maxX - minX, maxY - minY
}

func TestRotatedCanvasTextTurnsItsGlyphs(t *testing.T) {
	w, h := glyphBounds(t, 0)
	if w <= h {
		t.Fatalf("upright text is %.0f wide and %.0f tall", w, h)
	}
	w, h = glyphBounds(t, math.Pi/2)
	if h <= w {
		t.Errorf("text turned a quarter is %.0f wide and %.0f tall", w, h)
	}
}

func TestEachFingerKeepsItsOwnSlop(t *testing.T) {
	r := &Renderer{events: make(chan gui.Event, 16), still: map[uint64][2]float32{}}
	slop := gui.CurrentTheme().Cfg.TextStyleDef.Size
	r.Touch(Began, 1, 100, 100)
	r.Touch(Began, 2, 400, 100)
	r.Touch(Moved, 1, 100+slop/2, 100)
	r.Touch(Moved, 2, 400+slop*2, 100)
	r.Touch(Ended, 1, 100, 100)
	r.Touch(Moved, 2, 400+slop*3, 100)

	var got []struct {
		kind gui.EventType
		id   uint64
	}
	for len(r.events) > 0 {
		e := <-r.events
		got = append(got, struct {
			kind gui.EventType
			id   uint64
		}{e.Type, e.Touches[0].Identifier})
	}
	want := []struct {
		kind gui.EventType
		id   uint64
	}{
		{gui.EventTouchesBegan, 1},
		{gui.EventTouchesBegan, 2},
		{gui.EventTouchesMoved, 2},
		{gui.EventTouchesEnded, 1},
		{gui.EventTouchesMoved, 2},
	}
	if len(got) != len(want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event %d: %v, want %v", i, got[i], want[i])
		}
	}
	if len(r.still) != 0 {
		t.Errorf("fingers still held: %v", r.still)
	}
}

// The text system keeps each font it was given as bytes in a temp file; a closed renderer takes
// them with it, so a device's temp dir does not fill a few files at every start.
func TestRegressionAClosedRendererLeavesNoTempFonts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	w := gui.SimpleWindow("test", 320, 200, &app{}, func(w *gui.Window) { w.SetView(view) })
	r, err := New(&sink{}, w)
	if err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(dir); len(left) == 0 {
		t.Fatal("no temp fonts were written, so this test proves nothing")
	}

	r.Close()
	r.Close()

	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("%d temp files left after Close", len(left))
	}
}

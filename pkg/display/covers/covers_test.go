package covers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"
)

func TestABiggerAskGetsANewImage(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 640, 360))); err != nil {
		t.Fatal(err)
	}
	c := New(Options{
		Shape: Wide,
		Get:   func(context.Context, string) ([]byte, error) { return buf.Bytes(), nil },
	})
	settle := func(side int, not string) string {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if src := c.Src("https://example.com/frame", side); src != "" && src != not {
				return src
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("no new picture at %d", side)
		return ""
	}
	small := settle(100, "")
	large := settle(400, small)
	if mi, ok := gogui.LookupImage(large); !ok || mi.W != 400 {
		t.Errorf("the larger picture is %d wide (%v), want 400", mi.W, ok)
	}
	if gogui.HasImage(strings.TrimPrefix(small, "mem:")) {
		t.Error("the smaller picture was kept")
	}
}

func TestPicturesOnScreenOutliveOnesScrolledPast(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 32, 18))); err != nil {
		t.Fatal(err)
	}
	gets := map[string]int{}
	var mu sync.Mutex
	c := New(
		Options{
			Prefix: "screen",
			Shape:  Wide,
			Keep:   8,
			Get: func(_ context.Context, url string) ([]byte, error) {
				mu.Lock()
				gets[url]++
				mu.Unlock()
				return buf.Bytes(), nil
			},
		},
	)
	const onScreen = 5
	shown := 0
	for frame := range 40 {
		shown = 0
		for i := range onScreen {
			if c.Src(fmt.Sprintf("https://example.com/screen/%d", i), 32) != "" {
				shown++
			}
		}
		c.Src(fmt.Sprintf("https://example.com/past/%d", frame), 32)
		time.Sleep(2 * time.Millisecond)
	}
	if shown != onScreen {
		t.Errorf("%d of %d pictures on screen showing", shown, onScreen)
	}
	mu.Lock()
	defer mu.Unlock()
	for i := range onScreen {
		if n := gets[fmt.Sprintf("https://example.com/screen/%d", i)]; n != 1 {
			t.Errorf("picture %d on screen downloaded %d times", i, n)
		}
	}
}

func TestOnlyAFewPicturesDownloadAtOnce(t *testing.T) {
	var (
		running, most atomic.Int32
		release       = make(chan struct{})
		done          sync.WaitGroup
	)
	c := New(Options{Get: func(context.Context, string) ([]byte, error) {
		defer done.Done()
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		<-release
		running.Add(-1)
		return nil, errors.New("not a picture")
	}})
	done.Add(parallel)
	for i := range 3 * parallel {
		c.Src(fmt.Sprintf("https://example.com/%d", i), 64)
	}
	deadline := time.Now().Add(time.Second)
	for running.Load() < parallel && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(release)
	done.Wait()
	if got := most.Load(); got != parallel {
		t.Errorf("%d downloads ran at once, want %d", got, parallel)
	}
}

package avatar

import (
	"context"
	"hash/fnv"
	"image"
	"image/color"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	gogui "github.com/go-gui-org/go-gui/gui"
	xdraw "golang.org/x/image/draw"

	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/people"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

const (
	stale  = 24 * time.Hour
	limit  = 15 * time.Second
	pixels = 192
)

var fills = []gogui.Color{
	gogui.RGB(0x3b, 0x7d, 0xd8),
	gogui.RGB(0x2e, 0x9e, 0x6b),
	gogui.RGB(0xc2, 0x5b, 0x2c),
	gogui.RGB(0x8e, 0x4f, 0xc7),
	gogui.RGB(0xc2, 0x3f, 0x6a),
	gogui.RGB(0x1f, 0x8f, 0x9e),
	gogui.RGB(0xa6, 0x7c, 0x1a),
	gogui.RGB(0x5c, 0x6b, 0x8a),
}

type Person = people.Person

type Source interface {
	People(ctx context.Context) ([]Person, error)
	Picture(ctx context.Context, path string) (image.Image, error)
}

type Book struct {
	src    Source
	redraw func()

	mu      sync.Mutex
	people  []Person
	faces   map[string]*image.NRGBA
	loaded  time.Time
	loading bool
}

func NewBook(src Source, redraw func()) *Book {
	return &Book{src: src, redraw: redraw, faces: map[string]*image.NRGBA{}}
}

func (b *Book) People() []Person {
	b.mu.Lock()
	old := time.Since(b.loaded) > stale
	people := slices.Clone(b.people)
	b.mu.Unlock()
	if old {
		b.Refresh()
	}
	return people
}

func (b *Book) Refresh() {
	b.mu.Lock()
	if b.loading {
		b.mu.Unlock()
		return
	}
	b.loading = true
	b.mu.Unlock()
	safe.Go("people", b.load)
}

func (b *Book) load() {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	people, err := b.src.People(ctx)
	if err != nil {
		slog.Warn("listing people failed", "err", err)
		b.mu.Lock()
		b.loading = false
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	have := b.faces
	b.mu.Unlock()
	faces := map[string]*image.NRGBA{}
	whole := true
	for _, p := range people {
		if p.Picture == "" {
			continue
		}
		if f, ok := have[p.Picture]; ok {
			faces[p.Picture] = f
			continue
		}
		img, err := b.src.Picture(ctx, p.Picture)
		if err != nil {
			slog.Warn("fetching a person's picture failed", "person", p.ID, "err", err)
			whole = false
			continue
		}
		faces[p.Picture] = round(img)
	}
	b.mu.Lock()
	b.people, b.faces, b.loading = people, faces, false
	if whole {
		b.loaded = time.Now()
	}
	b.mu.Unlock()
	b.redraw()
}

func (b *Book) Badge(id, name string, size float32) gogui.View {
	b.mu.Lock()
	var face *image.NRGBA
	key := ""
	for _, p := range b.people {
		if p.ID == id && p.Picture != "" {
			face, key = b.faces[p.Picture], p.Picture
		}
	}
	b.mu.Unlock()
	if face != nil {
		return gogui.Image(gogui.ImageCfg{
			Src:    widgets.ImageSource("avatar:"+key, face),
			Width:  size,
			Height: size,
		})
	}
	st := gogui.CurrentTheme().TextStyleTitle
	st.Size = size * 0.42
	st.Color = gogui.RGB(0xff, 0xff, 0xff)
	return gogui.Row(gogui.ContainerCfg{
		Width:   size,
		Height:  size,
		Sizing:  gogui.FixedFixed,
		Padding: gogui.NoPadding,
		Radius:  gogui.RadiusPx(size / 2),
		Color:   fill(id),
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Content: []gogui.View{gogui.Label(Initials(name), st)},
	})
}

func Initials(name string) string {
	var out []rune
	for _, word := range strings.Fields(name) {
		r := []rune(word)[0]
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, unicode.ToUpper(r))
		}
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

func fill(id string) gogui.Color {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return fills[h.Sum32()%uint32(len(fills))]
}

func round(img image.Image) *image.NRGBA {
	b := img.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(image.Pt(
		b.Min.X+(b.Dx()-side)/2,
		b.Min.Y+(b.Dy()-side)/2,
	))
	out := image.NewNRGBA(image.Rect(0, 0, pixels, pixels))
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, crop, xdraw.Src, nil)
	r := float64(pixels) / 2
	for y := range pixels {
		for x := range pixels {
			dx, dy := float64(x)+0.5-r, float64(y)+0.5-r
			edge := r - math.Hypot(dx, dy)
			switch {
			case edge >= 1:
			case edge <= 0:
				out.SetNRGBA(x, y, color.NRGBA{})
			default:
				c := out.NRGBAAt(x, y)
				c.A = uint8(float64(c.A) * edge)
				out.SetNRGBA(x, y, c)
			}
		}
	}
	return out
}

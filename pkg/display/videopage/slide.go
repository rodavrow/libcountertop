package videopage

import (
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/nowplaying"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

const (
	slideTime  = 180 * time.Millisecond
	slideFrame = 16 * time.Millisecond
	slideShare = 4
)

type slide struct {
	move     func(dy int)
	from     float32
	dy       int
	height   int
	arriving int
	gen      int
}

func (p *Page) SlideWith(move func(dy int)) {
	p.mu.Lock()
	p.slide.move = move
	p.mu.Unlock()
}

func (p *Page) Slides() bool {
	p.mu.Lock()
	c, moves := p.c, p.slide.move != nil
	p.mu.Unlock()
	return moves && c != nil && c.Now().Short
}

func (p *Page) DragStart(y float32) {
	p.mu.Lock()
	p.slide.from = y
	p.slide.gen++
	p.mu.Unlock()
}

func (p *Page) DragTo(y float32, height int) {
	p.mu.Lock()
	p.slide.height = height
	dy := int(y - p.slide.from)
	p.mu.Unlock()
	p.place(dy)
}

func (p *Page) DragEnd(fling float32, height int) {
	p.mu.Lock()
	dy, c := p.slide.dy, p.c
	p.slide.height = height
	p.mu.Unlock()
	can := c.Now().Can
	switch {
	case (fling < 0 || dy < -height/slideShare) && can.Has(nowplaying.CanNext):
		p.leave(-height, 1, c.Next)
	case (fling > 0 || dy > height/slideShare) && can.Has(nowplaying.CanPrevious):
		p.leave(height, -1, c.Previous)
	default:
		p.animate(dy, 0, nil)
	}
}

func (p *Page) leave(to, arriving int, then func()) {
	p.mu.Lock()
	dy := p.slide.dy
	p.mu.Unlock()
	p.animate(dy, to, func() {
		p.mu.Lock()
		p.slide.arriving = arriving
		p.mu.Unlock()
		then()
	})
}

func (p *Page) arrive() {
	p.mu.Lock()
	arriving, height, dy := p.slide.arriving, p.slide.height, p.slide.dy
	p.slide.arriving = 0
	p.mu.Unlock()
	switch {
	case arriving != 0:
		p.animate(arriving*height, 0, nil)
	case dy != 0:
		p.animate(dy, 0, nil)
	}
}

func (p *Page) place(dy int) {
	p.mu.Lock()
	p.slide.dy = dy
	move := p.slide.move
	p.mu.Unlock()
	if move != nil {
		move(dy)
	}
}

func (p *Page) animate(from, to int, then func()) {
	p.mu.Lock()
	p.slide.gen++
	gen := p.slide.gen
	p.mu.Unlock()
	safe.Go("video slide", func() {
		began := time.Now()
		tick := time.NewTicker(slideFrame)
		defer tick.Stop()
		for {
			p.mu.Lock()
			current := p.slide.gen == gen
			p.mu.Unlock()
			if !current {
				return
			}
			t := min(float64(time.Since(began))/float64(slideTime), 1)
			eased := 1 - (1-t)*(1-t)
			p.place(from + int(float64(to-from)*eased))
			if t >= 1 {
				if then != nil {
					then()
				}
				return
			}
			<-tick.C
		}
	})
}

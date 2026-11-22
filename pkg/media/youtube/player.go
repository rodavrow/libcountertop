package youtube

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/nowplaying"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/media/sponsorblock"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

const (
	skipPoll  = 50 * time.Millisecond
	skipEarly = 100 * time.Millisecond
	skipWait  = 2 * time.Minute
	drainPoll = 200 * time.Millisecond
	upShown   = 12

	streamAhead = 2

	reportEvery = 20 * time.Second
)

type PlayerOptions struct {
	Resolver *Resolver
	Output   playback.Output
	Skips    func(ctx context.Context, id string) []sponsorblock.Segment
	Related  func(ctx context.Context, id string) []Video
	Autoplay func() bool
	Label    string
	Logo     string
	Changed  func()
}

type Player struct {
	o   PlayerOptions
	ctx context.Context

	mu      sync.Mutex
	cur     *source
	handed  *source
	opening context.CancelFunc
	list    []Video
	index   int
	related []Video
	more    func(context.Context) []Video
}

type source struct {
	*Track
	p     *Player
	video Video
}

var (
	_ playback.Source   = (*source)(nil)
	_ playback.Pictures = (*source)(nil)
	_ playback.Seeker   = (*source)(nil)
	_ playback.Marked   = (*source)(nil)
	_ playback.Branded  = (*source)(nil)
)

func NewPlayer(ctx context.Context, o PlayerOptions) *Player {
	return &Player{o: o, ctx: ctx}
}

func (p *Player) Play(list []Video, index int) {
	p.PlayStream(list, index, nil)
}

func (p *Player) PlayStream(list []Video, index int, more func(context.Context) []Video) {
	if index < 0 || index >= len(list) {
		return
	}
	p.mu.Lock()
	p.list, p.index, p.related, p.more = slices.Clone(list), index, nil, more
	p.mu.Unlock()
	p.open(list[index], list[index].Resume, false)
}

func (p *Player) Stop() {
	p.end(false)
	p.mu.Lock()
	p.list, p.index, p.related, p.more = nil, 0, nil, nil
	p.mu.Unlock()
	p.changed()
}

func (p *Player) Playing() (Video, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur == nil && p.opening == nil || p.index >= len(p.list) {
		return Video{}, false
	}
	return p.list[p.index], true
}

func (p *Player) Starting() (Video, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.opening == nil || p.index >= len(p.list) {
		return Video{}, false
	}
	return p.list[p.index], true
}

func (p *Player) Upcoming() []Video {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.upcomingLocked()
}

func (p *Player) upcomingLocked() []Video {
	var out []Video
	if p.index+1 < len(p.list) {
		out = append(out, p.list[p.index+1:]...)
	}
	for _, v := range p.related {
		if !slices.ContainsFunc(p.list, func(l Video) bool { return l.ID == v.ID }) {
			out = append(out, v)
		}
	}
	return out[:min(len(out), upShown)]
}

func (p *Player) Choose(v Video) {
	p.mu.Lock()
	if i := slices.IndexFunc(p.list, func(l Video) bool { return l.ID == v.ID }); i >= 0 {
		p.index = i
	} else {
		at := min(p.index+1, len(p.list))
		p.list = slices.Insert(p.list, at, v)
		p.index = at
	}
	p.mu.Unlock()
	p.open(v, 0, false)
}

func (p *Player) step(by int) {
	p.mu.Lock()
	up := p.upcomingLocked()
	switch {
	case by < 0 && p.index > 0:
		p.index--
		v := p.list[p.index]
		p.mu.Unlock()
		p.open(v, 0, false)
	case by > 0 && len(up) > 0:
		p.mu.Unlock()
		p.Choose(up[0])
	default:
		p.mu.Unlock()
	}
}

func (p *Player) around() (prev, next bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.index > 0, len(p.upcomingLocked()) > 0
}

func (p *Player) open(v Video, at time.Duration, held bool) {
	p.reopen(v, at, held, nil)
}

func (p *Player) reopen(v Video, at time.Duration, held bool, reports *watch) {
	p.end(true)
	ctx, cancel := context.WithCancel(p.ctx)
	p.mu.Lock()
	p.opening = cancel
	p.mu.Unlock()
	p.changed()

	safe.Go("youtube open", func() {
		s := &source{p: p, video: v}
		var skips func(context.Context) []sponsorblock.Segment
		if p.o.Skips != nil {
			skips = func(ctx context.Context) []sponsorblock.Segment { return p.o.Skips(ctx, v.ID) }
		}
		t, err := Open(ctx, p.o.Resolver, v.ID, Options{
			reports:  reports,
			At:       at,
			Skips:    skips,
			Finished: func() { p.finished(s) },
			Skip:     func(seg sponsorblock.Segment) { p.skipWhenHeard(s, seg) },
			Seek:     func(to time.Duration) { p.seek(s, to) },
		})
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("youtube playback", "video", v.ID, "err", err)
				p.mu.Lock()
				p.opening, p.handed = nil, nil
				p.mu.Unlock()
				p.o.Output.Failed(err)
				p.changed()
			}
			return
		}
		s.Track = t
		p.mu.Lock()
		if ctx.Err() != nil {
			p.mu.Unlock()
			t.Close()
			return
		}
		p.cur, p.opening, p.handed = s, nil, nil
		p.mu.Unlock()

		slog.Info("youtube playing", "video", v.ID, "title", t.Info().Title, "at", at)
		p.o.Output.Play(s)
		if held {
			p.o.Output.Pause(s)
		}
		p.changed()
		safe.Go("youtube watch reports", func() { p.reporting(s) })
		p.relate(ctx, s)
	})
}

func (p *Player) reporting(s *source) {
	tick := time.NewTicker(reportEvery)
	defer tick.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-tick.C:
		}
		if p.current() != s {
			return
		}
		if !p.paused(s) {
			p.report(s)
		}
	}
}

func (p *Player) report(s *source) {
	at, _ := p.o.Output.Position(s)
	s.report(at)
}

func (p *Player) relate(ctx context.Context, s *source) {
	p.mu.Lock()
	more, near := p.more, p.index+streamAhead >= len(p.list)
	p.mu.Unlock()
	if more != nil {
		if near {
			p.extend(ctx, s, more(ctx))
		}
		return
	}
	if p.o.Related == nil {
		return
	}
	related := p.o.Related(ctx, s.video.ID)
	related = slices.DeleteFunc(related, func(v Video) bool { return v.ID == s.video.ID })
	p.mu.Lock()
	if p.cur != s {
		p.mu.Unlock()
		return
	}
	p.related = related
	p.mu.Unlock()
	p.o.Output.Changed(s)
	p.changed()
}

func (p *Player) extend(ctx context.Context, s *source, got []Video) {
	p.mu.Lock()
	if p.cur != s || ctx.Err() != nil {
		p.mu.Unlock()
		return
	}
	for _, v := range got {
		if !slices.ContainsFunc(p.list, func(l Video) bool { return l.ID == v.ID }) {
			p.list = append(p.list, v)
		}
	}
	p.mu.Unlock()
	p.o.Output.Changed(s)
	p.changed()
}

func (p *Player) end(handoff bool) {
	p.mu.Lock()
	cur, handed, opening := p.cur, p.handed, p.opening
	p.cur, p.opening = nil, nil
	switch {
	case handoff && cur != nil:
		p.handed = cur
	case !handoff:
		p.handed = nil
	}
	p.mu.Unlock()
	if opening != nil {
		opening()
	}
	if cur != nil {
		p.report(cur)
	}
	switch {
	case handoff && cur != nil:
		p.o.Output.Handoff(cur)
	case !handoff && cur != nil:
		p.o.Output.Stop(cur)
	case !handoff && handed != nil:
		p.o.Output.Stop(handed)
	}
	if cur != nil {
		cur.Close()
	}
}

func (p *Player) stop(s *source) {
	p.mu.Lock()
	ours := s == p.cur || s == p.handed
	p.mu.Unlock()
	if ours {
		p.Stop()
	}
}

func (p *Player) current() *source {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cur
}

func (p *Player) paused(s *source) bool {
	_, paused := p.o.Output.Position(s)
	return paused
}

func (p *Player) seek(s *source, to time.Duration) {
	if p.current() != s {
		return
	}
	p.reopen(s.video, to, p.paused(s), s.reports)
}

func (p *Player) finished(s *source) {
	for p.o.Output.Behind(s) > 0 {
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(drainPoll):
		}
	}
	if p.current() != s {
		return
	}
	if p.advances() {
		p.step(1)
		return
	}
	p.Stop()
}

func (p *Player) advances() bool {
	_, next := p.around()
	return next && p.o.Autoplay != nil && p.o.Autoplay()
}

func (p *Player) skipWhenHeard(s *source, seg sponsorblock.Segment) {
	until := time.Now().Add(skipWait)
	for time.Now().Before(until) {
		if p.current() != s || p.ctx.Err() != nil {
			return
		}
		if at, _ := p.o.Output.Position(s); at >= seg.From-skipEarly {
			break
		}
		time.Sleep(skipPoll)
	}
	slog.Info("youtube sponsorblock skip", "video", s.video.ID, "from", seg.From, "to", seg.To)
	p.seek(s, seg.To)
}

func (p *Player) changed() {
	if p.o.Changed != nil {
		p.o.Changed()
	}
}

func (s *source) Showing() playback.Showing {
	sh := s.Track.Showing()
	if sh.Art == "" {
		sh.Art = s.video.Thumbnail.URL
	}
	sh.HasPrevious, sh.HasNext = s.p.around()
	sh.Short = s.video.Kind == Short
	for _, v := range s.p.Upcoming() {
		sh.Upcoming = append(sh.Upcoming, playback.Upcoming{
			Title:  v.Title,
			Artist: v.Channel,
			Length: v.Length,
			Art:    v.Thumbnail.URL,
			Play:   func() { s.p.Choose(v) },
		})
	}
	return sh
}

func (s *source) Label() string { return s.p.o.Label }

func (s *source) Kind() nowplaying.Kind { return nowplaying.FromApp }

func (s *source) Logo() string { return s.p.o.Logo }

func (s *source) Play() { s.p.o.Output.Resume(s) }
func (s *source) Pause() {
	s.p.o.Output.Pause(s)
	s.p.report(s)
}
func (s *source) Stop()     { go s.p.stop(s) }
func (s *source) Next()     { go s.p.step(1) }
func (s *source) Previous() { go s.p.step(-1) }

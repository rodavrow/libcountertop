package plex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/cast"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/media/urlplayer"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

const (
	reportEvery = 10 * time.Second
	watchEvery  = time.Second
	reportTime  = 10 * time.Second
)

type PlayerOptions struct {
	Output   playback.Output
	Target   func() playback.Target
	Decoder  Decoder
	Resample func(from int) func(stereo []int16) []int16
	HTTP     *http.Client
	Changed  func()
}

type Player struct {
	o PlayerOptions

	mu      sync.Mutex
	player  *urlplayer.Player
	playing *playing
	failed  *Item
}

type playing struct {
	lib     *Library
	item    Item
	session string
	cancel  context.CancelFunc
	ended   bool
}

func NewPlayer(o PlayerOptions) *Player { return &Player{o: o} }

func (p *Player) Play(lib *Library, it Item, from time.Duration) error {
	p.stopReporting(Stopped)
	p.mu.Lock()
	p.failed = nil
	p.mu.Unlock()
	session := newSession()
	asking, done := context.WithTimeout(context.Background(), callTime)
	s, err := lib.Stream(asking, it, p.o.Decoder, session)
	done()
	if err != nil {
		return err
	}
	media := cast.Media{
		ContentID:   s.URL,
		ContentURL:  s.URL,
		ContentType: s.ContentType,
		StreamType:  "BUFFERED",
		Duration:    it.Duration.Seconds(),
		Metadata:    metadata(lib, it),
	}
	p.mu.Lock()
	if p.player == nil {
		p.player = urlplayer.New(urlplayer.Options{
			HTTP:     p.o.HTTP,
			Output:   p.o.Output,
			Resample: p.o.Resample,
			Screen:   it.Type != "track",
			Target:   p.decoded,
			Ready:    p.changed,
			Finished: p.finished,
		})
	}
	player := p.player
	ctx, cancel := context.WithCancel(context.Background())
	now := &playing{lib: lib, item: it, session: session, cancel: cancel}
	p.playing = now
	p.mu.Unlock()
	if err := player.Load(media, from, true); err != nil {
		cancel()
		return err
	}
	safe.Go("plex timeline", func() { p.follow(ctx, player, now) })
	p.changed()
	return nil
}

func (p *Player) decoded() playback.Target {
	var t playback.Target
	if p.o.Target != nil {
		t = p.o.Target()
	}
	if p.o.Decoder.Height > 0 {
		t.Tallest = p.o.Decoder.Height
	}
	return t
}

func (p *Player) Stop() {
	p.mu.Lock()
	player := p.player
	p.mu.Unlock()
	if player != nil {
		_ = player.Stop()
	}
	p.stopReporting(Stopped)
}

func (p *Player) Now() (Item, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.playing == nil {
		return Item{}, false
	}
	return p.playing.item, true
}

func (p *Player) Starting() (Item, bool) {
	p.mu.Lock()
	now, player := p.playing, p.player
	p.mu.Unlock()
	if now == nil || player == nil || player.Ready() {
		return Item{}, false
	}
	return now.item, true
}

func (p *Player) Failed() (Item, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed == nil {
		return Item{}, false
	}
	return *p.failed, true
}

func (p *Player) finished() {
	p.mu.Lock()
	if p.playing != nil {
		p.playing.ended = true
	}
	p.mu.Unlock()
	p.stopReporting(Stopped)
}

func (p *Player) follow(ctx context.Context, player *urlplayer.Player, now *playing) {
	watch := time.NewTicker(watchEvery)
	defer watch.Stop()
	var last time.Time
	var seen bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-watch.C:
		}
		if err := player.Failed(); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("plex play", "item", now.item.RatingKey, "err", err)
			p.mu.Lock()
			p.failed = &now.item
			p.mu.Unlock()
			p.stopReporting(Stopped)
			return
		}
		_, shown := player.Shown()
		if shown {
			seen = true
		}
		if seen && !shown {
			p.stopReporting(Stopped)
			return
		}
		if !shown || time.Since(last) < reportEvery {
			continue
		}
		last = time.Now()
		state := Playing
		if player.Paused() {
			state = Paused
		}
		p.report(now, state, player.Elapsed())
	}
}

func (p *Player) stopReporting(st State) {
	p.mu.Lock()
	now := p.playing
	p.playing = nil
	player := p.player
	p.mu.Unlock()
	if now == nil {
		return
	}
	now.cancel()
	at := now.item.Duration
	if !now.ended && player != nil {
		at = player.Elapsed()
	}
	p.report(now, st, at)
	p.changed()
}

func (p *Player) report(now *playing, st State, at time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), reportTime)
	defer cancel()
	if err := now.lib.Report(ctx, now.item, st, at, now.item.Duration, now.session); err != nil {
		slog.Warn("plex timeline", "item", now.item.RatingKey, "state", st, "err", err)
	}
}

func (p *Player) changed() {
	if p.o.Changed != nil {
		p.o.Changed()
	}
}

func metadata(lib *Library, it Item) cast.Metadata {
	md := cast.Metadata{Title: it.Title}
	switch it.Type {
	case "episode":
		md.Subtitle = it.Show
	case "track":
		md.Artist = it.Show
		md.AlbumName = it.Season
	}
	if pic := lib.Picture(it.Thumb, posterWidth, posterHeight); pic != "" {
		md.Images = []cast.Image{{URL: pic}}
	}
	return md
}

const (
	posterWidth  = 480
	posterHeight = 720
)

func newSession() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

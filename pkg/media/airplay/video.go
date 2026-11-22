package airplay

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ygelfand/libcountertop/pkg/audio/resample"
	"github.com/ygelfand/libcountertop/pkg/fetch"
	"github.com/ygelfand/libcountertop/pkg/media/cast"
	"github.com/ygelfand/libcountertop/pkg/media/urlplayer"
	"github.com/ygelfand/libcountertop/pkg/say"
)

const (
	reportEvery = 500 * time.Millisecond
	nextItemFor = 5 * time.Second
)

type video struct {
	player  *urlplayer.Player
	end     atomic.Int64
	item    atomic.Int64
	cancel  context.CancelFunc
	workers sync.WaitGroup
}

func (s *screening) stream(p play) {
	media := cast.Media{
		ContentID:  p.location,
		StreamType: cast.StreamBuffered,
		Metadata:   cast.Metadata{Title: s.client},
	}
	if strings.HasSuffix(p.location, ".m3u8") {
		media.ContentType = "application/x-mpegurl"
	}
	slog.Info("airplay video", "client", s.client, "location", p.location, "at", p.at)
	if v := s.streaming(); v != nil {
		v.item.Add(1)
		v.end.Store(0)
		if err := v.player.Load(media, p.at, true); err != nil {
			slog.Warn("airplay video refused", "location", p.location, "err", err)
			s.over()
			s.report(position{state: stateOver})
		}
		return
	}
	s.over()
	o := s.r.opts
	v := &video{}
	out := o.Output(func() { v.end.Store(int64(stateOver)) })
	v.player = urlplayer.New(urlplayer.Options{
		HTTP:   fetch.Client(0),
		Output: out,
		Resample: func(from int) func([]int16) []int16 {
			return resample.NewRational(from, o.Rate, o.Channels).Run
		},
		Screen:   o.Video != nil,
		Target:   o.Video,
		Finished: func() { v.end.CompareAndSwap(0, int64(stateEnded)) },
	})
	if err := v.player.Load(media, p.at, true); err != nil {
		slog.Warn("airplay video refused", "location", p.location, "err", err)
		s.report(position{state: stateOver})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	s.mu.Lock()
	s.video = v
	s.mu.Unlock()
	v.workers.Go(func() { s.follow(ctx, v) })
}

func (s *screening) follow(ctx context.Context, v *video) {
	tick := time.NewTicker(reportEvery)
	defer tick.Stop()
	for {
		s.report(v.position())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (v *video) position() position {
	var length time.Duration
	if shown, ok := v.player.Shown(); ok {
		length = shown.Length
	}
	switch state(v.end.Load()) {
	case stateOver:
		return position{state: stateOver}
	case stateEnded:
		return position{at: length, length: length, state: stateEnded}
	}
	if !v.player.Ready() {
		return position{state: stateLoading}
	}
	p := position{at: v.player.Elapsed(), length: length, state: statePlaying}
	if v.player.Paused() {
		p.state = statePaused
	}
	return p
}

func (v *video) stop() {
	v.cancel()
	v.workers.Wait()
	_ = v.player.Stop()
}

func (s *screening) between() {
	v := s.streaming()
	if v == nil {
		return
	}
	item := v.item.Load()
	v.player.Release()
	time.AfterFunc(nextItemFor, func() {
		if s.streaming() == v && v.item.Load() == item {
			slog.Info("airplay video stopped", "client", s.client)
			s.over()
		}
	})
}

func (s *screening) scrub(at time.Duration) {
	if v := s.streaming(); v != nil {
		if err := v.player.Seek(at); err != nil {
			slog.Info("airplay video seek refused", "at", at, "err", err)
		}
	}
}

func (s *screening) rate(playing bool) {
	v := s.streaming()
	if v == nil {
		return
	}
	if playing {
		_ = v.player.Play()
	} else {
		_ = v.player.Pause()
	}
}

func (s *screening) streaming() *video {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.video
}

func language() string {
	if l := say.Chosen(); l != "" {
		return l
	}
	return say.English.String()
}

func (s *screening) report(p position) {
	s.write.Lock()
	defer s.write.Unlock()
	if err := writeMessage(s.conn, msgPosition, p.bytes()); err != nil {
		slog.Debug("airplay position not sent", "err", err)
	}
}

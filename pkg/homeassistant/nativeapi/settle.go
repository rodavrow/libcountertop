package nativeapi

import (
	"context"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"
)

const (
	// settleWithin is how long Home Assistant gets from listing the entities to subscribing to what
	// it set up. It takes a few seconds even on a busy start.
	settleWithin = time.Minute
	// settleMost caps the wait as it grows, for a Home Assistant that stalls every time.
	settleMost = 10 * time.Minute
)

// settle drops a connection on which Home Assistant's setup of the device stalled halfway: it
// listed the entities but never subscribed to their states, so every entity stays unavailable
// while the rest works. After a Home Assistant restart its setup was seen to hang until the
// connection went, and to come up within seconds on the next one.
//
// A satellite's setup also subscribes to voice turns. Missing that is retried only once:
// Home Assistant does the same when its assist satellite is turned off, and a stall it keeps
// across connections needs its ESPHome entry reloaded.
type settle struct {
	within, most, every time.Duration
	busy                func() bool

	mu         sync.Mutex
	satellite  bool
	wait       time.Duration
	retried    bool
	connecting map[*esphome.Conn]*setup
}

// setup is what Home Assistant has subscribed to on one connection so far.
type setup struct {
	states, voice bool
	timer         *time.Timer
}

func newSettle(busy func() bool) *settle {
	return &settle{
		within:     settleWithin,
		most:       settleMost,
		every:      busyEvery,
		busy:       busy,
		wait:       settleWithin,
		connecting: map[*esphome.Conn]*setup{},
	}
}

var _ esphome.ConnCloser = (*settle)(nil)

// expectVoice says whether the device is a voice satellite, whose setup is not done until Home
// Assistant subscribes to its turns.
func (s *settle) expectVoice(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.satellite = on
}

func (s *settle) Handle(_ context.Context, c *esphome.Conn, msg proto.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch m := msg.(type) {
	case *api.ListEntitiesRequest:
		if _, ok := s.connecting[c]; !ok {
			p := &setup{}
			p.timer = time.AfterFunc(s.wait, func() { s.check(c) })
			s.connecting[c] = p
		}
	case *api.SubscribeStatesRequest:
		if p, ok := s.connecting[c]; ok {
			p.states = true
			s.settled(c, p)
		}
	case *api.SubscribeVoiceAssistantRequest:
		if p, ok := s.connecting[c]; ok && m.GetSubscribe() {
			p.voice = true
			s.settled(c, p)
		}
	}
	return nil
}

func (s *settle) ConnClosed(c *esphome.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.connecting[c]; ok {
		p.timer.Stop()
		delete(s.connecting, c)
	}
}

// settled forgets a connection once Home Assistant has subscribed to all it sets up.
func (s *settle) settled(c *esphome.Conn, p *setup) {
	if !p.states || (s.satellite && !p.voice) {
		return
	}
	p.timer.Stop()
	delete(s.connecting, c)
	s.wait = s.within
	s.retried = false
}

func (s *settle) check(c *esphome.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.connecting[c]
	if !ok {
		return
	}
	// Closing ends a turn under way; voice may work while states do not.
	if s.busy != nil && s.busy() {
		p.timer.Reset(s.every)
		return
	}
	delete(s.connecting, c)
	switch {
	case !p.states:
		slog.Warn("Home Assistant never subscribed to the entities' states, reconnecting",
			"client", c.ClientInfo(), "waited", s.wait)
		s.wait = min(2*s.wait, s.most)
	case s.retried:
		slog.Warn("Home Assistant still has not subscribed to voice turns; if the assist "+
			"satellite is meant to be on, reload the device's ESPHome entry in Home Assistant",
			"client", c.ClientInfo())
		return
	default:
		slog.Warn("Home Assistant never subscribed to voice turns, reconnecting once",
			"client", c.ClientInfo(), "waited", s.wait)
		s.retried = true
	}
	_ = c.Close()
}

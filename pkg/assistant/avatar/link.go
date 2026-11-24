package avatar

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	display "github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/media/call"
	"github.com/ygelfand/libcountertop/pkg/media/rtc"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

// State is how a wake word's LiveTalking session is doing, as Home Assistant sees it.
type State string

const (
	Off        State = "off"
	Connecting State = "connecting"
	Connected  State = "connected"
	Failed     State = "failed"
)

var errLost = errors.New("avatar: the LiveTalking connection was lost")

// peer is the WebRTC side of a session; rtc.Viewer in the device.
type peer interface {
	Offer(ctx context.Context) (string, error)
	Answered(answer string) error
	Deafen(on bool)
	RequestKey()
	Close() error
}

type dialer func(
	show func(rtc.Frame),
	play func([]int16),
	report func(webrtc.PeerConnectionState),
) (peer, error)

// screen is the hardware video layer the avatar is shown on; callvideo.Stream in the device.
type screen interface {
	Show(rtc.Frame)
	Place(display.Rect, int)
	Hide()
	Delay(time.Duration)
	Refused() bool
	Stats() call.LayerStats
	Close()
}

// link is one wake word's LiveTalking session and what it shows and plays.
type link struct {
	slot int
	a    *Avatars

	mu      sync.Mutex
	cfg     schema.Avatar
	state   State
	why     error
	session string
	peer    peer
	gen     uint64
	since   time.Time
	retryAt time.Time
	backoff time.Duration
	holds   int
	closeAt time.Time
	speech  *speech

	// The decoder is fed only while someone wants the picture: primed hidden until it has one,
	// then placed on screen.
	scr       screen
	feeding   bool
	visible   bool
	placedAt  time.Time
	needKey   bool
	shownAt   time.Time
	shownBase uint64
	pictured  bool
	stalled   bool
	published [2]string
}

func (l *link) snapshot() (State, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state, l.session
}

// reconcile moves the session toward what the settings and the holds want, and keeps the video
// honest. It runs on every tick and after every change.
func (l *link) reconcile(now time.Time, cfg schema.Avatar, busy bool) {
	l.mu.Lock()
	wanted := cfg.Ready() && (cfg.Connection.Always() || l.holds > 0 || now.Before(l.closeAt))
	open := l.state == Connecting || l.state == Connected
	moved := cfg.URL != l.cfg.URL || cfg.ID != l.cfg.ID
	var closing peer
	switch {
	case open && (!wanted || moved):
		closing = l.drop(Off, nil)
	case l.state == Failed && (!wanted || moved):
		l.state, l.why, l.backoff = Off, nil, 0
	case l.state == Connecting && now.Sub(l.since) > l.a.timing.connect:
		closing = l.fail(fmt.Errorf("avatar: no connection after %s", l.a.timing.connect))
	}
	var start func()
	if wanted && (l.state == Off || (l.state == Failed && !now.Before(l.retryAt))) {
		start = l.connect(now, cfg)
	}
	l.hideIf(now, busy)
	l.mu.Unlock()
	l.close(closing)
	l.publish()
	if start != nil {
		l.a.spawn(start)
	}
}

// hideIf takes the video off the screen when nothing placed it lately, another video needs the
// screen, or the picture stalled; a stall holds until the next wake.
func (l *link) hideIf(now time.Time, busy bool) {
	if !l.feeding {
		return
	}
	stale := now.Sub(l.placedAt) > l.a.timing.unplaced
	// Only a picture that was up and stopped is a stall; until the first keyframe arrives the
	// normal look simply stays.
	if !l.stalled && l.pictured && now.Sub(l.shownAt) > l.a.timing.stall {
		slog.Warn("avatar video stalled, showing the normal look", "slot", l.slot+1)
		l.stalled = true
	}
	if stale || busy || l.stalled || l.state != Connected {
		l.rest()
	}
}

// rest stops feeding the decoder and takes the picture off the screen. Callers hold mu.
func (l *link) rest() {
	l.feeding, l.visible, l.pictured = false, false, false
	if l.scr != nil {
		l.scr.Hide()
	}
}

// connect marks the link connecting and returns the work that opens it, to run once mu is
// released. Callers hold mu.
func (l *link) connect(now time.Time, cfg schema.Avatar) func() {
	l.gen++
	gen := l.gen
	l.cfg, l.state, l.why, l.since, l.session = cfg, Connecting, nil, now, ""
	slog.Info("avatar connecting", "slot", l.slot+1, "url", cfg.URL, "avatar", cfg.ID)
	return func() { l.open(gen, cfg) }
}

func (l *link) open(gen uint64, cfg schema.Avatar) {
	p, err := l.a.dial(
		func(f rtc.Frame) { l.frame(gen, f) },
		func(pcm []int16) { l.heard(gen, pcm) },
		func(s webrtc.PeerConnectionState) { l.report(gen, s) },
	)
	if err != nil {
		l.failed(gen, err)
		return
	}
	p.Deafen(true)
	ctx, cancel := context.WithTimeout(context.Background(), l.a.timing.connect)
	defer cancel()
	offer, err := p.Offer(ctx)
	if err == nil {
		var answer, id string
		answer, id, err = l.a.server(cfg).offer(ctx, offer, cfg.ID)
		if err == nil {
			err = p.Answered(answer)
		}
		if err == nil {
			if !l.opened(gen, p, id) {
				_ = p.Close()
			}
			return
		}
	}
	_ = p.Close()
	l.failed(gen, err)
}

// opened keeps the peer unless the link moved on while it was connecting.
func (l *link) opened(gen uint64, p peer, id string) bool {
	l.mu.Lock()
	if gen != l.gen {
		l.mu.Unlock()
		return false
	}
	l.peer, l.session = p, id
	l.mu.Unlock()
	slog.Info("avatar answered", "slot", l.slot+1, "session", id)
	l.publish()
	return true
}

func (l *link) report(gen uint64, s webrtc.PeerConnectionState) {
	switch s {
	case webrtc.PeerConnectionStateConnected:
		l.mu.Lock()
		if gen == l.gen && l.state == Connecting {
			l.state, l.backoff = Connected, 0
			slog.Info("avatar connected", "slot", l.slot+1, "session", l.session)
		}
		l.mu.Unlock()
		l.publish()
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateDisconnected,
		webrtc.PeerConnectionStateClosed:
		l.failed(gen, fmt.Errorf("%w: %s", errLost, s))
	}
}

func (l *link) failed(gen uint64, err error) {
	l.mu.Lock()
	if gen != l.gen || l.state == Off || l.state == Failed {
		l.mu.Unlock()
		return
	}
	closing := l.fail(err)
	l.mu.Unlock()
	l.close(closing)
	l.publish()
}

// fail ends the session and schedules a retry with backoff. Callers hold mu.
func (l *link) fail(err error) peer {
	slog.Warn("avatar connection failed", "slot", l.slot+1, "err", err)
	closing := l.drop(Failed, err)
	l.backoff = min(max(2*l.backoff, l.a.timing.retry), l.a.timing.maxRetry)
	l.retryAt = l.a.now().Add(l.backoff)
	return closing
}

// drop forgets the session and returns its peer for the caller to close after unlocking, as
// closing waits for the callbacks that take mu. Callers hold mu.
func (l *link) drop(to State, why error) peer {
	p := l.peer
	l.gen++
	l.peer, l.session, l.state, l.why = nil, "", to, why
	if l.speech != nil {
		l.speech.stop(false)
	}
	if l.feeding {
		l.rest()
	}
	return p
}

func (l *link) close(p peer) {
	if p == nil {
		return
	}
	l.a.spawn(func() {
		if err := p.Close(); err != nil {
			slog.Debug("avatar close", "slot", l.slot+1, "err", err)
		}
	})
}

func (l *link) publish() {
	l.mu.Lock()
	now := [2]string{string(l.state), l.session}
	changed := now != l.published
	l.published = now
	l.mu.Unlock()
	if changed && l.a.published != nil {
		l.a.published(l.slot, State(now[0]), now[1])
	}
}

// frame feeds the video layer while the avatar is on screen; otherwise the decoder rests.
func (l *link) frame(gen uint64, f rtc.Frame) {
	l.mu.Lock()
	if gen != l.gen || !l.feeding || l.scr == nil {
		l.mu.Unlock()
		return
	}
	if l.needKey && !f.Key {
		p := l.peer
		l.mu.Unlock()
		if p != nil {
			p.RequestKey()
		}
		return
	}
	l.needKey = false
	scr := l.scr
	l.mu.Unlock()

	scr.Show(f)

	shown := scr.Stats().Shown
	l.mu.Lock()
	if l.feeding && shown > l.shownBase {
		l.shownAt, l.shownBase, l.pictured = l.a.now(), shown, true
	}
	l.mu.Unlock()
}

func (l *link) heard(gen uint64, pcm []int16) {
	l.mu.Lock()
	s := l.speech
	if gen != l.gen {
		s = nil
	}
	l.mu.Unlock()
	if s != nil {
		s.hear(pcm)
	}
}

// prime feeds the decoder, hidden, so the picture is ready before it is shown; it reports whether
// the picture is up, and until it is the caller keeps drawing the normal look.
func (l *link) prime(busy bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if busy || l.stalled || l.state != Connected || !l.cfg.Ready() {
		return false
	}
	if l.scr == nil {
		if l.scr = l.a.screen(); l.scr == nil {
			return false
		}
	}
	if l.scr.Refused() {
		return false
	}
	now := l.a.now()
	l.placedAt = now
	if !l.feeding {
		l.feeding, l.visible, l.pictured, l.needKey, l.shownAt = true, false, false, true, now
		l.shownBase = l.scr.Stats().Shown
		l.scr.Hide()
		if p := l.peer; p != nil {
			l.a.spawn(p.RequestKey)
		}
	}
	return l.pictured
}

// place shows the primed picture in box at depth z; false while there is none yet.
func (l *link) place(box display.Rect, z int, busy bool) bool {
	if !l.prime(busy) {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.feeding || l.scr == nil {
		return false
	}
	l.visible = true
	l.scr.Place(box, z)
	return true
}

func (l *link) hold() {
	l.mu.Lock()
	l.holds++
	l.closeAt, l.stalled = time.Time{}, false
	l.mu.Unlock()
}

func (l *link) release(linger time.Duration) {
	l.mu.Lock()
	if l.holds > 0 {
		l.holds--
	}
	if l.holds == 0 {
		l.closeAt = l.a.now().Add(linger)
	}
	l.mu.Unlock()
}

func (l *link) shut() {
	l.mu.Lock()
	closing := l.drop(Off, nil)
	scr := l.scr
	l.scr = nil
	l.mu.Unlock()
	l.close(closing)
	if scr != nil {
		scr.Close()
	}
}

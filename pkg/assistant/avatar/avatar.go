package avatar

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/ygelfand/libcountertop/pkg/display/callvideo"
	display "github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/display/surface"
	"github.com/ygelfand/libcountertop/pkg/media/rtc"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const (
	// DefaultLayer is the avatar's hardware video layer; calls, cameras and doorbells use others.
	DefaultLayer = 940
	// DefaultZ keeps the avatar below call video.
	DefaultZ = -3

	maxClip = 32 << 20
)

type Options struct {
	Slots int
	Read  func() schema.Wake
	Write func(slot int) schema.WakeWriter

	// DeviceID puts slot's entities on its assistant sub-device.
	DeviceID func(slot int) uint32

	// Client reaches LiveTalking and fetches reply clips; pass the device's routed client so
	// local-only Internet keeps working.
	Client *http.Client

	// Display is where the video goes; nil plays the avatar's voice with the normal look.
	Display callvideo.Display
	Layer   uint32
	Z       int

	// OutputLatency is how long the board's speaker takes to sound what the voice lane queued,
	// beyond the queue itself; the video waits as long to stay in sync with the lips.
	OutputLatency time.Duration

	// VideoBusy reports that a call, camera, cast or other video owns the screen.
	VideoBusy func() bool
}

type timing struct {
	tick, connect, retry, maxRetry time.Duration
	linger, unplaced, stall        time.Duration
	waitLink, firstSound, poll     time.Duration
	quiet, silent, most            time.Duration
}

var defaults = timing{
	tick:       100 * time.Millisecond,
	connect:    15 * time.Second,
	retry:      time.Second,
	maxRetry:   30 * time.Second,
	linger:     10 * time.Second,
	unplaced:   400 * time.Millisecond,
	stall:      2 * time.Second,
	waitLink:   3 * time.Second,
	firstSound: 4 * time.Second,
	poll:       150 * time.Millisecond,
	quiet:      300 * time.Millisecond,
	silent:     3 * time.Second,
	most:       10 * time.Minute,
}

// Avatars are the wake words' LiveTalking avatars.
type Avatars struct {
	o      Options
	links  []*link
	ents   []entities
	timing timing

	now       func() time.Time
	dial      dialer
	screen    func() screen
	spawn     func(func())
	published func(slot int, s State, session string)
	changed   chan struct{}
}

func New(o Options) *Avatars {
	if o.Layer == 0 {
		o.Layer, o.Z = DefaultLayer, DefaultZ
	}
	if o.Client == nil {
		o.Client = http.DefaultClient
	}
	a := &Avatars{
		o:       o,
		timing:  defaults,
		now:     time.Now,
		dial:    dialViewer,
		spawn:   func(f func()) { go f() },
		changed: make(chan struct{}, 1),
	}
	a.screen = a.stream
	for n := range o.Slots {
		a.links = append(a.links, &link{slot: n, a: a, state: Off})
		a.ents = append(a.ents, a.newEntities(n))
	}
	a.published = a.show
	return a
}

func dialViewer(
	show func(rtc.Frame),
	play func([]int16),
	report func(webrtc.PeerConnectionState),
) (peer, error) {
	return rtc.NewViewer(show, play, report)
}

func (a *Avatars) stream() screen {
	if a.o.Display == nil {
		return nil
	}
	return callvideo.NewStream(a.o.Display, surface.AVC, a.o.Layer, a.o.Z)
}

func (a *Avatars) Name() string { return "avatars" }

func (a *Avatars) server(cfg schema.Avatar) server {
	return server{client: a.o.Client, base: cfg.URL}
}

func (a *Avatars) slot(n int) (schema.Avatar, bool) {
	if n < 0 || n >= len(a.links) {
		return schema.Avatar{}, false
	}
	return a.o.Read().Slot(n).Avatar, true
}

func (a *Avatars) busy() bool { return a.o.VideoBusy != nil && a.o.VideoBusy() }

// Run keeps the sessions where the settings want them until ctx ends, then closes them.
func (a *Avatars) Run(ctx context.Context) {
	t := time.NewTicker(a.timing.tick)
	defer t.Stop()
	defer func() {
		for _, l := range a.links {
			l.shut()
		}
	}()
	for {
		a.reconcile()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.changed:
		}
	}
}

func (a *Avatars) reconcile() {
	now, busy := a.now(), a.busy()
	wake := a.o.Read()
	for n, l := range a.links {
		l.reconcile(now, wake.Slot(n).Avatar, busy)
	}
}

func (a *Avatars) poke() {
	select {
	case a.changed <- struct{}{}:
	default:
	}
}

// Wants reports whether slot's replies go to its avatar, so they must arrive as whole clips.
func (a *Avatars) Wants(slot int) bool {
	cfg, ok := a.slot(slot)
	return ok && cfg.Ready()
}

// Wake holds slot's session open for a turn or an announcement, opening it if need be; every
// Wake is paired with a Rest.
func (a *Avatars) Wake(slot int) {
	if slot < 0 || slot >= len(a.links) {
		return
	}
	a.links[slot].hold()
	a.poke()
}

// Rest lets slot's session close a little later, unless it is always connected.
func (a *Avatars) Rest(slot int) {
	if slot < 0 || slot >= len(a.links) {
		return
	}
	a.links[slot].release(a.timing.linger)
	a.poke()
}

// Place shows slot's avatar in box, in screen coordinates, and reports whether its picture is
// up; while it is not, the caller draws the normal look.
func (a *Avatars) Place(slot int, box display.Rect) bool {
	if slot < 0 || slot >= len(a.links) {
		return false
	}
	return a.links[slot].place(box, a.busy())
}

// Speak has slot's avatar say the clip at url, playing its voice into sink, and returns once it
// is done. handled is false when the clip was not spoken and the caller should play it itself:
// the link is down, LiveTalking refused it, or no sound came back in time.
func (a *Avatars) Speak(
	ctx context.Context,
	slot int,
	url string,
	sink Sink,
	started func(),
) (handled bool, err error) {
	cfg, ok := a.slot(slot)
	if !ok || !cfg.Ready() || url == "" {
		return false, nil
	}
	l := a.links[slot]
	id, gen, ok := a.connected(ctx, l)
	if !ok {
		slog.Info("avatar not connected, speaking locally", "slot", slot+1)
		return false, nil
	}
	clip, err := a.fetch(ctx, url)
	if err != nil {
		slog.Warn("avatar could not fetch the reply, speaking locally", "slot", slot+1, "err", err)
		return false, nil
	}

	sp := newSpeech(sink, started, a.now)
	p := a.attach(l, gen, sp)
	if p == nil {
		return false, nil
	}
	defer a.detach(l, sp)

	srv := a.server(cfg)
	if err := srv.speak(ctx, id, clip, clipName(url)); err != nil {
		if ctx.Err() != nil {
			// Stopped during the upload: LiveTalking may have started speaking anyway.
			sp.stop(true)
			interruptNow(srv, id, slot)
			return true, nil
		}
		slog.Warn("avatar refused the reply, speaking locally", "slot", slot+1, "err", err)
		return false, nil
	}
	return a.follow(ctx, l, srv, id, gen, sp)
}

// connected waits a little for a session that is still connecting.
func (a *Avatars) connected(ctx context.Context, l *link) (id string, gen uint64, ok bool) {
	give := a.now().Add(a.timing.waitLink)
	for {
		l.mu.Lock()
		state, id, gen := l.state, l.session, l.gen
		l.mu.Unlock()
		if state == Connected && id != "" {
			return id, gen, true
		}
		if (state != Connecting && state != Off) || !a.now().Before(give) {
			return "", 0, false
		}
		select {
		case <-ctx.Done():
			return "", 0, false
		case <-time.After(a.timing.poll):
		}
	}
}

func (a *Avatars) attach(l *link, gen uint64, sp *speech) peer {
	l.mu.Lock()
	defer l.mu.Unlock()
	if gen != l.gen || l.peer == nil {
		return nil
	}
	if l.speech != nil {
		l.speech.stop(false)
	}
	l.speech = sp
	l.peer.Deafen(false)
	return l.peer
}

func (a *Avatars) detach(l *link, sp *speech) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.speech != sp {
		return
	}
	l.speech = nil
	if l.peer != nil {
		l.peer.Deafen(true)
	}
	if l.scr != nil {
		l.scr.Delay(0)
	}
}

// follow waits for the reply to end: LiveTalking says it stopped and nothing audible came for a
// moment, since its flag turns false before the last of the audio arrives.
func (a *Avatars) follow(
	ctx context.Context,
	l *link,
	srv server,
	id string,
	gen uint64,
	sp *speech,
) (bool, error) {
	sent := a.now()
	t := time.NewTicker(a.timing.poll)
	defer t.Stop()
	interrupt := func() { interruptNow(srv, id, l.slot) }
	for {
		select {
		case <-ctx.Done():
			sp.stop(true)
			interrupt()
			return true, nil
		case <-t.C:
		}
		now := a.now()
		first, last := sp.heard()
		if lost := a.lost(l, gen); lost {
			if first.IsZero() {
				slog.Warn("avatar lost before it spoke, speaking locally", "slot", l.slot+1)
				return false, nil
			}
			return true, errLost
		}
		a.sync(l, sp)
		switch {
		case first.IsZero() && now.Sub(sent) > a.timing.firstSound:
			slog.Warn("avatar sent no sound, speaking locally", "slot", l.slot+1)
			sp.stop(true)
			interrupt()
			return false, nil
		case first.IsZero():
			continue
		case now.Sub(sent) > a.timing.most:
			interrupt()
			return true, fmt.Errorf("avatar: still speaking after %s", a.timing.most)
		case now.Sub(last) > a.timing.silent:
			return true, nil
		case now.Sub(last) < a.timing.quiet:
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, time.Second)
		on, err := srv.speaking(pctx, id)
		cancel()
		if err != nil {
			slog.Debug("avatar is_speaking", "slot", l.slot+1, "err", err)
			continue
		}
		if !on {
			return true, nil
		}
	}
}

// interruptNow stops the session speaking, outside the reply's context, which may be done.
func interruptNow(srv server, id string, slot int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.interrupt(ctx, id); err != nil {
		slog.Debug("avatar interrupt", "slot", slot+1, "err", err)
	}
}

func (a *Avatars) lost(l *link, gen uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return gen != l.gen || l.state != Connected
}

// sync holds the video back as long as the voice takes to reach the speaker.
func (a *Avatars) sync(l *link, sp *speech) {
	l.mu.Lock()
	scr := l.scr
	l.mu.Unlock()
	if scr != nil {
		scr.Delay(sp.latency() + a.o.OutputLatency)
	}
}

func (a *Avatars) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	clip, err := io.ReadAll(io.LimitReader(resp.Body, maxClip+1))
	if err != nil {
		return nil, err
	}
	if len(clip) > maxClip {
		return nil, fmt.Errorf("%s: longer than %d bytes", url, maxClip)
	}
	return clip, nil
}

// clipName keeps the clip's file name, as LiveTalking reads the format from its extension.
func clipName(raw string) string {
	if u, err := neturl.Parse(raw); err == nil && u.Path != "" && u.Path != "/" {
		return u.Path
	}
	return "reply.mp3"
}

package avatar

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/ygelfand/libcountertop/pkg/assistant/turn"
	"github.com/ygelfand/libcountertop/pkg/display/callvideo"
	display "github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/display/surface"
	"github.com/ygelfand/libcountertop/pkg/media/rtc"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const (
	// DefaultLayer is the avatar's hardware video layer; calls, cameras and doorbells use others.
	DefaultLayer = 940
	// DefaultZ is the full-screen avatar's depth: where the full-screen look sits, below the
	// interface and above the photo screen's GPU layer (-1). It gives way to call and camera
	// video through VideoBusy rather than by depth.
	DefaultZ = 1
	// DefaultZAbove is the assistant look's depth over the interface.
	DefaultZAbove = 6

	maxClip = 32 << 20
)

type Options struct {
	Slots int
	Read  func() schema.Wake
	Write func(slot int) schema.WakeWriter

	// DeviceID puts slot's entities on its assistant sub-device.
	DeviceID func(slot int) uint32

	// Client reaches LiveTalking and fetches reply clips. Give it a timeout, as fetch.Client
	// does, or a hung server holds a reply until it is stopped; pass the device's routed client
	// so local-only Internet keeps working.
	Client *http.Client

	// Display is where the video goes; nil plays the avatar's voice with the normal look.
	Display callvideo.Display
	Layer   uint32
	// Z is the avatar's depth below the interface, for a look across the screen; ZAbove its
	// depth over the interface, for a look in a panel.
	Z, ZAbove int

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

var _ turn.Avatar = (*Avatars)(nil)

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

	// The avatars share one hardware layer, as a second stream on it would close the first
	// one's decoder; owner is the link whose stream scr is.
	vmu   sync.Mutex
	scr   screen
	owner *link
}

// claim hands l the shared layer and stops whichever avatar drove it before; nil when there is
// no display. A new owner gets a fresh stream: the last one's queued frames, clock, shown count
// and lip-sync delay belong to another session, and closing it takes its picture down.
func (a *Avatars) claim(l *link) screen {
	a.vmu.Lock()
	prev := a.owner
	if prev != l && a.scr != nil {
		a.scr.Close()
		a.scr, a.owner = nil, nil
	}
	if a.scr == nil {
		a.scr = a.screen()
	}
	if a.scr != nil {
		a.owner = l
	}
	scr := a.scr
	a.vmu.Unlock()
	if prev != nil && prev != l {
		prev.yield()
	}
	return scr
}

// owns reports whether l drives the shared layer.
func (a *Avatars) owns(l *link) bool {
	a.vmu.Lock()
	defer a.vmu.Unlock()
	return a.owner == l
}

func (a *Avatars) closeScreen() {
	a.vmu.Lock()
	scr := a.scr
	a.scr, a.owner = nil, nil
	a.vmu.Unlock()
	if scr != nil {
		scr.Close()
	}
}

func New(o Options) *Avatars {
	if o.Layer == 0 {
		o.Layer, o.Z, o.ZAbove = DefaultLayer, DefaultZ, DefaultZAbove
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
		a.closeScreen()
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

// Chosen opens slot's session as soon as Home Assistant names it for the next announcement, and
// lets it go when that choice is used or forgotten; pass it as announcements.Options.Chosen.
func (a *Avatars) Chosen(slot int, on bool) {
	if on {
		a.Wake(slot)
		return
	}
	a.Rest(slot)
}

// Showing readies slot's picture without showing it and reports whether it is up; while it is
// not, the caller draws the normal look. Ask on every frame the look is wanted.
func (a *Avatars) Showing(slot int) bool {
	if slot < 0 || slot >= len(a.links) {
		return false
	}
	return a.links[slot].prime(a.busy())
}

// Place shows slot's avatar in box, in screen coordinates, and reports whether its picture is
// up; while it is not, the caller draws the normal look. above puts it over the interface, for
// a look in a panel; otherwise it sits below, where the interface is clear.
func (a *Avatars) Place(slot int, box display.Rect, above bool) bool {
	if slot < 0 || slot >= len(a.links) {
		return false
	}
	z := a.o.Z
	if above {
		z = a.o.ZAbove
	}
	return a.links[slot].place(box, z, a.busy())
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
		slog.Info("avatar not ready, speaking locally", "slot", slot+1, "clip", url != "")
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
		slog.Info("avatar session changed, speaking locally", "slot", slot+1)
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
	sent := a.now()
	handled, err = a.follow(ctx, l, srv, id, gen, sp)
	if handled {
		slog.Info("avatar spoke", "slot", slot+1, "session", id, "took", a.now().Sub(sent),
			"err", err)
	}
	return handled, err
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
	if l.scr != nil && a.owns(l) {
		l.scr.Delay(0)
	}
	l.delayOn = nil
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

// sync holds the video back as long as the voice takes to reach the speaker. Within a reply the
// delay only grows, so the picture never jumps back and stutters.
func (a *Avatars) sync(l *link, sp *speech) {
	l.mu.Lock()
	scr := l.scr
	fresh := scr != l.delayOn
	l.mu.Unlock()
	if scr == nil || !a.owns(l) {
		return
	}
	// A stream the reply has not delayed yet, after taking the layer back, needs the delay
	// already reached, not only growth.
	if d, grew := sp.delay(sp.latency() + a.o.OutputLatency); grew || fresh {
		scr.Delay(d)
		l.mu.Lock()
		l.delayOn = scr
		l.mu.Unlock()
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

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

	"github.com/ygelfand/libcountertop/pkg/assistant/turn"
	"github.com/ygelfand/libcountertop/pkg/display/callvideo"
	display "github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/display/surface"
	"github.com/ygelfand/libcountertop/pkg/hook"
	"github.com/ygelfand/libcountertop/pkg/media/rtc"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const (
	// DefaultLayer is the first wake word's hardware video layer, and each next wake word's is
	// one higher; calls, cameras and doorbells use others, the doorbell 940.
	DefaultLayer = 950
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
	// A reply Home Assistant feeds: how long its first sound may take once it is open, how long
	// it may go quiet before it counts as broken off, how far before the break the clip resumes,
	// and how long the clip's first bytes may take.
	fedFirstSound, fedSilent, rewind, backup time.Duration
	pending, hold, heldUnplaced              time.Duration
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

	fedFirstSound: 3 * time.Second,
	fedSilent:     1500 * time.Millisecond,
	rewind:        300 * time.Millisecond,
	backup:        30 * time.Second,
	pending:       time.Second,
	hold:          time.Minute,
	// The screen redraws a held avatar only once a second.
	heldUnplaced: 2 * time.Second,
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
	screen    func(slot int) screen
	spawn     func(func())
	published func(slot int, s State, session string)
	changed   chan struct{}

	hold hold

	// HeldChanged fires when the avatar held between announcements changes or is let go, so the
	// screen redraws at once rather than at its next tick.
	HeldChanged hook.Hook[struct{}]
}

// placed makes l the avatar on screen and retires every other, closing its decoder, so two run
// only while one hands over to the other.
func (a *Avatars) placed(l *link) {
	for _, other := range a.links {
		if other != l {
			other.retire()
		}
	}
}

// makeRoom closes the other avatars' decoders for l, whose own could not open: a board with one
// decoder to spare hands over with a short gap instead. It reports whether one was closed.
func (a *Avatars) makeRoom(l *link) bool {
	closed := false
	for _, other := range a.links {
		if other != l && other.retire() {
			slog.Warn("avatar second stream refused, closing the first",
				"slot", l.slot+1, "first", other.slot+1)
			closed = true
		}
	}
	return closed
}

// lingering reports that slot's picture stays up, though nothing holds or places it, because
// the avatar taking over is still readying its own.
func (a *Avatars) lingering(slot int, now time.Time, busy bool) bool {
	next, ok := a.links[slot].successor(now)
	return ok && next >= 0 && next < len(a.links) && a.links[next].ready(busy)
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
		a.links = append(a.links, &link{slot: n, a: a, state: Off, next: -1})
		a.ents = append(a.ents, a.newEntities(n))
	}
	a.newHold()
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

func (a *Avatars) stream(slot int) screen {
	if a.o.Display == nil {
		return nil
	}
	return callvideo.NewStream(a.o.Display, surface.AVC, a.o.Layer+uint32(slot), a.o.Z)
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
	a.checkHold(now)
	held := a.heldSlot()
	wake := a.o.Read()
	for n, l := range a.links {
		keep := n == held || a.lingering(n, now, busy)
		l.reconcile(now, wake.Slot(n).Avatar, busy, keep)
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
	// A turn or announcement arriving takes over from an avatar held since the last one, once
	// its show begins.
	a.arrived()
	a.open(slot)
}

// open holds slot's session open without touching the avatar held on screen.
func (a *Avatars) open(slot int) {
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
		// Only naming the assistant: the avatar held on screen stays until it speaks.
		a.open(slot)
		return
	}
	a.Rest(slot)
}

// Pending reports that slot's picture is being readied and should be up within a moment, so the
// caller can leave the look's place empty rather than flash the normal look first. It turns false
// once the picture is up, when it takes too long, or when the link cannot show it.
func (a *Avatars) Pending(slot int) bool {
	if slot < 0 || slot >= len(a.links) || !a.Wants(slot) {
		return false
	}
	return a.links[slot].pending(a.now(), a.busy(), a.timing.pending)
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
// is done. handled is false when the device should speak the clip itself: the link is down,
// LiveTalking refused it, no sound came back in time, or a reply Home Assistant fed into the
// session broke off. rest is then what is left to say, at 16 kHz mono, or nil for the whole clip.
func (a *Avatars) Speak(
	ctx context.Context,
	slot int,
	url string,
	sink Sink,
	started func(),
) (handled bool, rest []int16, err error) {
	cfg, ok := a.slot(slot)
	if !ok || !cfg.Ready() || url == "" {
		slog.Info("avatar not ready, speaking locally", "slot", slot+1, "clip", url != "")
		return false, nil, nil
	}
	l := a.links[slot]
	a.replying(true)
	defer a.replying(false)
	id, gen, ok := a.connected(ctx, l)
	if !ok {
		slog.Info("avatar not connected, speaking locally", "slot", slot+1)
		return false, nil, nil
	}
	if cfg.Fed {
		return a.speakFed(ctx, l, cfg, id, gen, url, sink, started)
	}
	handled, err = a.speakClip(ctx, l, cfg, id, gen, url, sink, started)
	return handled, nil, err
}

// speakClip sends the whole clip to the session and follows it as LiveTalking speaks it.
func (a *Avatars) speakClip(
	ctx context.Context,
	l *link,
	cfg schema.Avatar,
	id string,
	gen uint64,
	url string,
	sink Sink,
	started func(),
) (handled bool, err error) {
	slot := l.slot
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
	if handled && err == nil && ctx.Err() == nil && l.onScreen() {
		a.spoke(slot)
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
	// One line per reply, to tell a reply that lost its opening on the device from one that
	// arrived without it.
	if first, flowing, handed := sp.voiced(); !first.IsZero() {
		slog.Info("avatar voice", "slot", l.slot+1,
			"first_audible", first.UTC().Format("15:04:05.000"),
			"speaker_from", flowing.UTC().Format("15:04:05.000"),
			"handed", handed.Round(10*time.Millisecond))
	}
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
	speaking := func() (bool, error) {
		pctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		return srv.speaking(pctx, id)
	}
	// began is that LiveTalking has said it is speaking the clip. Until then, for as long as it
	// may take to start, sound on the stream is not the reply and a quiet moment is not its end.
	began := false
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
		starting := !began && now.Sub(sent) <= a.timing.firstSound
		if starting {
			if on, err := speaking(); err == nil && on {
				began, starting = true, false
			}
		}
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
		case starting:
			continue
		case now.Sub(last) > a.timing.silent:
			return true, nil
		case now.Sub(last) < a.timing.quiet:
			continue
		}
		on, err := speaking()
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
	if scr == nil {
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

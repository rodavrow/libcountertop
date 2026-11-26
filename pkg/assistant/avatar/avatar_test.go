package avatar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	esphome "github.com/ygelfand/go-esphome-device"

	display "github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/display/surface"
	"github.com/ygelfand/libcountertop/pkg/media/call"
	"github.com/ygelfand/libcountertop/pkg/media/rtc"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type memory struct {
	mu   sync.Mutex
	wake schema.Wake
}

func (m *memory) Read() schema.Wake {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wake
}

func (m *memory) Update(change func(*schema.Wake)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(&m.wake)
	return nil
}

type fakePeer struct {
	show   func(rtc.Frame)
	play   func([]int16)
	report func(webrtc.PeerConnectionState)

	deaf   atomic.Bool
	keys   atomic.Int32
	closed atomic.Bool
}

func (p *fakePeer) Offer(context.Context) (string, error) { return "v=0 offer", nil }
func (p *fakePeer) Answered(string) error                 { return nil }
func (p *fakePeer) Deafen(on bool)                        { p.deaf.Store(on) }
func (p *fakePeer) RequestKey()                           { p.keys.Add(1) }
func (p *fakePeer) Close() error                          { p.closed.Store(true); return nil }

type fakeScreen struct {
	mu      sync.Mutex
	shown   uint64
	placed  display.Rect
	hidden  bool
	delay   time.Duration
	refused bool
	z       int
	closed  bool
	slot    int
	// blockedBy is a stream whose decoder must close before this one's can open.
	blockedBy *fakeScreen
	failed    error
	// slow is how many samples the decoder turns away while it starts.
	slow int
}

func (s *fakeScreen) Show(rtc.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.refusedLocked() {
		return
	}
	if b := s.blockedBy; b != nil && !b.isClosed() {
		s.failed = surface.Broken
		return
	}
	if s.slow > 0 {
		// The decoder is still starting: the sample is not taken, which is no refusal.
		s.slow--
		s.failed = surface.Full
		return
	}
	s.shown++
}

func (s *fakeScreen) refusedLocked() bool {
	return s.refused || s.shown == 0 && errors.Is(s.failed, surface.Broken)
}

func (s *fakeScreen) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
func (s *fakeScreen) Place(r display.Rect, z int) {
	s.mu.Lock()
	s.placed, s.hidden, s.z = r, false, z
	s.mu.Unlock()
}
func (s *fakeScreen) Hide()                 { s.mu.Lock(); s.hidden = true; s.mu.Unlock() }
func (s *fakeScreen) Delay(d time.Duration) { s.mu.Lock(); s.delay = d; s.mu.Unlock() }
func (s *fakeScreen) Refused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refusedLocked()
}
func (s *fakeScreen) Stats() call.LayerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return call.LayerStats{Shown: s.shown, Failed: s.failed}
}
func (s *fakeScreen) Close() { s.mu.Lock(); s.closed = true; s.mu.Unlock() }

// liveTalking is a fake LiveTalking server.
type liveTalking struct {
	mu          sync.Mutex
	offers      int
	avatar      string
	spoken      [][]byte
	interrupted int
	speaking    bool
	uploaded    chan struct{}
	// reply is the streamed reply open on the session, as /is_speaking names it.
	reply string
	// failing is how many status calls fail before they answer again.
	failing int
	// gate holds the clip's first bytes back until it is closed.
	gate chan struct{}
}

func (lt *liveTalking) set(f func(*liveTalking)) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	f(lt)
}

func (lt *liveTalking) handler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/clip.mp3" {
		lt.mu.Lock()
		gate := lt.gate
		lt.mu.Unlock()
		if gate != nil {
			<-gate
		}
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	switch r.URL.Path {
	case "/clip.mp3":
		_, _ = io.WriteString(w, "ID3 clip")
	case "/offer":
		var o offerRequest
		_ = json.NewDecoder(r.Body).Decode(&o)
		lt.offers++
		lt.avatar = o.Avatar
		_, _ = io.WriteString(w, `{"sdp":"v=0 answer","type":"answer","sessionid":7}`)
	case "/humanaudio":
		f, _, err := r.FormFile("file")
		if err == nil {
			b, _ := io.ReadAll(f)
			lt.spoken = append(lt.spoken, b)
		}
		if lt.uploaded != nil {
			close(lt.uploaded)
			lt.uploaded = nil
		}
		_, _ = io.WriteString(w, `{"code":0}`)
	case "/interrupt_talk":
		lt.interrupted++
		_, _ = io.WriteString(w, `{"code":0}`)
	case "/is_speaking":
		if lt.failing > 0 {
			lt.failing--
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		reply := "null"
		if lt.reply != "" {
			reply = `"` + lt.reply + `"`
		}
		_, _ = fmt.Fprintf(w, `{"code":0,"data":%t,"reply_id":%s}`, lt.speaking, reply)
	default:
		http.NotFound(w, r)
	}
}

func baseOf(e esphome.Entity) *esphome.Base {
	switch v := e.(type) {
	case *esphome.Switch:
		return &v.Base
	case *esphome.Text:
		return &v.Base
	case *esphome.Select:
		return &v.Base
	case *esphome.TextSensor:
		return &v.Base
	}
	return &esphome.Base{}
}

type rig struct {
	a     *Avatars
	m     *memory
	lt    *liveTalking
	url   string
	clock *clock
	peers []*fakePeer
	scr   *fakeScreen
	busy  atomic.Bool
	pub   map[int][2]string

	uploaded chan struct{}
}

func newRig(t *testing.T, cfg schema.Avatar) *rig {
	t.Helper()
	r := &rig{m: &memory{}, lt: &liveTalking{uploaded: make(chan struct{})}, clock: &clock{at: time.Unix(1000, 0)}, scr: &fakeScreen{}}
	ts := httptest.NewServer(http.HandlerFunc(r.lt.handler))
	t.Cleanup(ts.Close)
	r.url = ts.URL
	if cfg.URL == "set" {
		cfg.URL = ts.URL
	}
	r.m.wake.Words = []schema.WakeWord{schema.DefaultWakeWord(), schema.DefaultWakeWord()}
	r.m.wake.Words[0].Avatar = cfg
	r.a = New(Options{
		Slots:     2,
		Read:      r.m.Read,
		Write:     func(slot int) schema.WakeWriter { return schema.NewWakeWriter(r.m, slot) },
		Client:    ts.Client(),
		VideoBusy: r.busy.Load,
	})
	r.a.now = r.clock.now
	r.a.spawn = func(f func()) { f() }
	r.a.dial = func(
		show func(rtc.Frame),
		play func([]int16),
		report func(webrtc.PeerConnectionState),
	) (peer, error) {
		p := &fakePeer{show: show, play: play, report: report}
		r.peers = append(r.peers, p)
		return p, nil
	}
	r.a.screen = func(int) screen { return r.scr }
	r.uploaded = r.lt.uploaded
	r.pub = map[int][2]string{}
	show := r.a.published
	r.a.published = func(slot int, s State, session string) {
		r.pub[slot] = [2]string{string(s), session}
		show(slot, s, session)
	}
	return r
}

func (r *rig) peer() *fakePeer { return r.peers[len(r.peers)-1] }

func (r *rig) connect(t *testing.T) {
	t.Helper()
	r.a.reconcile()
	if len(r.peers) == 0 {
		t.Fatal("no connection opened")
	}
	r.peer().report(webrtc.PeerConnectionStateConnected)
	if s, _ := r.a.links[0].snapshot(); s != Connected {
		t.Fatalf("state %s, want connected", s)
	}
}

func always() schema.Avatar {
	return schema.Avatar{On: true, URL: "set", ID: "sylvia", Connection: schema.AvatarAlways}
}

func woken() schema.Avatar {
	return schema.Avatar{On: true, URL: "set", Connection: schema.AvatarWhenWoken}
}

func TestAnAlwaysConnectedAvatarOpensItsSessionAndPublishesIt(t *testing.T) {
	r := newRig(t, always())

	r.connect(t)

	if r.lt.offers != 1 || r.lt.avatar != "sylvia" {
		t.Errorf("offers %d for avatar %q", r.lt.offers, r.lt.avatar)
	}
	if got := r.pub[0]; got != [2]string{"connected", "7"} {
		t.Errorf("published %v", got)
	}
	if got := r.a.ents[0].session.Get(); got != "7" {
		t.Errorf("session sensor %q", got)
	}
	if !r.peer().deaf.Load() {
		t.Error("the idle stream's audio is decoded")
	}
}

func TestAnAvatarThatIsOffOpensNothing(t *testing.T) {
	cfg := always()
	cfg.On = false
	r := newRig(t, cfg)

	r.a.reconcile()

	if len(r.peers) != 0 || r.a.Wants(0) {
		t.Fatalf("opened %d sessions, wants %v", len(r.peers), r.a.Wants(0))
	}
}

func TestAWhenWokenAvatarConnectsOnWakeAndClosesAfterTheLinger(t *testing.T) {
	r := newRig(t, woken())
	r.a.reconcile()
	if len(r.peers) != 0 {
		t.Fatal("connected before a wake")
	}

	r.a.Wake(0)
	r.connect(t)
	r.a.Rest(0)
	r.clock.pass(defaults.linger - time.Second)
	r.a.reconcile()
	if r.peer().closed.Load() {
		t.Fatal("closed before the linger")
	}
	r.clock.pass(2 * time.Second)
	r.a.reconcile()

	if !r.peer().closed.Load() {
		t.Fatal("still open after the linger")
	}
	if got := r.pub[0]; got != [2]string{"off", ""} {
		t.Errorf("published %v", got)
	}
}

func TestAWakeDuringTheLingerKeepsTheSession(t *testing.T) {
	r := newRig(t, woken())
	r.a.Wake(0)
	r.connect(t)
	r.a.Rest(0)
	r.clock.pass(defaults.linger / 2)
	r.a.Wake(0)
	r.clock.pass(defaults.linger)
	r.a.reconcile()

	if r.peer().closed.Load() || len(r.peers) != 1 {
		t.Fatalf("closed %v, %d sessions", r.peer().closed.Load(), len(r.peers))
	}
}

func TestALostConnectionRetriesWithBackoff(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)

	r.peer().report(webrtc.PeerConnectionStateFailed)
	if s, _ := r.a.links[0].snapshot(); s != Failed {
		t.Fatalf("state %s, want failed", s)
	}
	r.a.reconcile()
	if len(r.peers) != 1 {
		t.Fatal("retried at once")
	}
	r.clock.pass(defaults.retry)
	r.a.reconcile()

	if len(r.peers) != 2 {
		t.Fatalf("%d connections, want a retry", len(r.peers))
	}
}

// A new session is a new video stream: fed into the decoder left from the old one, it showed
// one stale frame and stalled on the first wake after LiveTalking restarted.
func TestRegressionAReconnectOpensANewStream(t *testing.T) {
	r := newRig(t, always())
	var opened []*fakeScreen
	r.a.screen = func(slot int) screen {
		s := &fakeScreen{slot: slot}
		opened = append(opened, s)
		return s
	}
	r.connect(t)
	r.a.Showing(0)
	r.peer().show(rtc.Frame{Key: true})

	r.peer().report(webrtc.PeerConnectionStateDisconnected)
	if len(opened) != 1 || !opened[0].isClosed() {
		t.Fatal("the lost session's stream stayed open")
	}
	r.clock.pass(defaults.retry)
	r.a.reconcile()
	again := r.peer()
	again.report(webrtc.PeerConnectionStateConnected)
	keys := again.keys.Load()

	r.a.Showing(0)
	if len(opened) != 2 {
		t.Fatalf("%d streams, want a new one for the new session", len(opened))
	}
	if again.keys.Load() == keys {
		t.Error("asked the new session for no keyframe")
	}
	again.show(rtc.Frame{Key: true})
	if !r.a.Place(0, display.Rect{W: 10, H: 10}, false) {
		t.Fatal("the first wake after the reconnect got no picture")
	}
}

func TestAChangedURLReconnects(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)

	if err := schema.NewWakeWriter(r.m, 0).AvatarURL(r.url + "/other"); err != nil {
		t.Fatal(err)
	}
	r.a.reconcile()

	if !r.peers[0].closed.Load() || len(r.peers) != 2 {
		t.Fatalf("first closed %v, %d connections", r.peers[0].closed.Load(), len(r.peers))
	}
}

func TestAConnectionThatNeverConnectsFails(t *testing.T) {
	r := newRig(t, always())
	r.a.reconcile()

	r.clock.pass(defaults.connect + time.Second)
	r.a.reconcile()

	if s, _ := r.a.links[0].snapshot(); s != Failed {
		t.Fatalf("state %s, want failed", s)
	}
}

func TestThePictureShowsOnlyFromAKeyframeOnceStaged(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	p := r.peer()
	box := display.Rect{X: 10, Y: 20, W: 300, H: 300}

	p.show(rtc.Frame{Key: true})
	if r.scr.shown != 0 {
		t.Fatal("fed the decoder before anyone wanted the picture")
	}
	if r.a.Showing(0) || r.a.Place(0, box, true) {
		t.Fatal("showing with no picture")
	}
	if p.keys.Load() == 0 {
		t.Error("asked for no keyframe when primed")
	}
	if !r.scr.hidden {
		t.Error("the primed layer is on screen")
	}
	p.show(rtc.Frame{})
	if r.scr.shown != 0 {
		t.Fatal("fed a delta frame before a keyframe")
	}
	p.show(rtc.Frame{Key: true})
	p.show(rtc.Frame{})

	if !r.a.Showing(0) || !r.a.Place(0, box, true) || r.scr.shown != 2 || r.scr.placed != box ||
		r.scr.hidden || r.scr.z != DefaultZAbove {
		t.Fatalf("shown %d at %+v, hidden %v", r.scr.shown, r.scr.placed, r.scr.hidden)
	}
}

func TestAFullScreenAvatarSitsBelowTheInterface(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	r.a.Showing(0)
	r.peer().show(rtc.Frame{Key: true})

	if !r.a.Place(0, display.Rect{W: 10, H: 10}, false) || r.scr.z != DefaultZ {
		t.Fatalf("placed at z %d, want %d", r.scr.z, DefaultZ)
	}
}

func TestAnUnplacedAvatarHidesAndStopsFeeding(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	r.a.Place(0, display.Rect{W: 10, H: 10}, true)
	r.peer().show(rtc.Frame{Key: true})

	r.clock.pass(defaults.unplaced + time.Millisecond)
	r.a.reconcile()
	r.peer().show(rtc.Frame{Key: true})

	if !r.scr.hidden || r.scr.shown != 1 {
		t.Fatalf("hidden %v, shown %d", r.scr.hidden, r.scr.shown)
	}
}

func TestAStalledPictureFallsBackUntilTheNextWake(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	box := display.Rect{W: 10, H: 10}
	r.a.Place(0, box, true)
	r.peer().show(rtc.Frame{Key: true})

	for range 30 {
		r.clock.pass(100 * time.Millisecond)
		r.a.Place(0, box, true)
		r.a.reconcile()
	}
	if r.a.Place(0, box, true) || !r.scr.hidden {
		t.Fatal("a stalled picture stays up")
	}

	r.a.Wake(0)
	r.a.Showing(0)
	r.peer().show(rtc.Frame{Key: true})
	if !r.a.Place(0, box, true) {
		t.Fatal("the picture did not come back on the next wake")
	}
}

func TestASlowFirstKeyframeIsNotAStall(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	box := display.Rect{W: 10, H: 10}

	for range 40 {
		r.clock.pass(100 * time.Millisecond)
		r.a.Showing(0)
		r.a.reconcile()
	}
	r.peer().show(rtc.Frame{Key: true})

	if !r.a.Place(0, box, true) {
		t.Fatal("a picture that took four seconds to arrive is not shown")
	}
}

// twoAvatars is a rig with both wake words in avatar mode, connected, where each avatar's stream
// is its own fake, as each has its own layer and decoder on the device.
func twoAvatars(t *testing.T) (r *rig, sylvia, orion *fakePeer, opened *[]*fakeScreen) {
	t.Helper()
	r = newRig(t, always())
	o := always()
	o.URL, o.ID = r.url, "orion"
	r.m.wake.Words[1].Avatar = o
	opened = &[]*fakeScreen{}
	r.a.screen = func(slot int) screen {
		s := &fakeScreen{slot: slot}
		*opened = append(*opened, s)
		return s
	}
	r.a.reconcile()
	if len(r.peers) != 2 {
		t.Fatalf("%d sessions, want 2", len(r.peers))
	}
	sylvia, orion = r.peers[0], r.peers[1]
	sylvia.report(webrtc.PeerConnectionStateConnected)
	orion.report(webrtc.PeerConnectionStateConnected)
	return r, sylvia, orion, opened
}

// heldOnScreen has slot 1's avatar on screen and held, with the next line arrived for slot next.
func heldOnScreen(t *testing.T, r *rig, sylvia *fakePeer, next int) {
	t.Helper()
	holdOn(r)
	r.a.Showing(0)
	sylvia.show(rtc.Frame{Key: true})
	if !r.a.Place(0, display.Rect{W: 10, H: 10}, false) {
		t.Fatal("no picture to hold")
	}
	r.a.spoke(0)
	r.a.Wake(next)
}

// Each avatar has its own layer, so the one on screen stays until the next one's picture is up:
// nothing shows through between two lines of a chat.
func TestOneAvatarHandsOverToAnotherWithoutAGap(t *testing.T) {
	r, sylvia, orion, opened := twoAvatars(t)
	heldOnScreen(t, r, sylvia, 1)
	box := display.Rect{W: 10, H: 10}

	r.a.Began(1)
	for range 6 {
		r.clock.pass(100 * time.Millisecond)
		sylvia.show(rtc.Frame{})
		r.a.Showing(1)
		r.a.reconcile()
	}
	first := (*opened)[0]
	if first.hidden || first.closed {
		t.Fatalf("the first avatar left before the next had a picture: hidden %v, closed %v",
			first.hidden, first.closed)
	}
	orion.show(rtc.Frame{Key: true})
	if !r.a.Place(1, box, false) {
		t.Fatal("the next avatar has no picture")
	}

	if len(*opened) != 2 || (*opened)[1].slot != 1 || (*opened)[1].hidden {
		t.Fatalf("streams %d, the next avatar's not up", len(*opened))
	}
	if !first.hidden || !first.closed {
		t.Errorf("the first avatar stayed under the next: hidden %v, closed %v",
			first.hidden, first.closed)
	}
}

func TestAnAvatarWaitingForTheNextDoesNotWaitLong(t *testing.T) {
	r, sylvia, _, opened := twoAvatars(t)
	heldOnScreen(t, r, sylvia, 1)

	r.a.Began(1)
	for range 13 {
		r.clock.pass(100 * time.Millisecond)
		sylvia.show(rtc.Frame{})
		r.a.Showing(1)
		r.a.reconcile()
	}

	if !(*opened)[0].hidden {
		t.Fatal("the first avatar is still up, though the next never showed a picture")
	}
}

func TestAnAvatarDoesNotWaitForOneThatCannotShow(t *testing.T) {
	r, sylvia, orion, opened := twoAvatars(t)
	heldOnScreen(t, r, sylvia, 1)
	r.a.Began(1)

	orion.report(webrtc.PeerConnectionStateFailed)
	r.clock.pass(defaults.unplaced + 100*time.Millisecond)
	r.a.reconcile()

	if !(*opened)[0].hidden {
		t.Fatal("the first avatar waits for one whose link dropped")
	}
}

func TestAnAvatarDoesNotWaitForAnAssistantWithoutOne(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	heldOnScreen(t, r, r.peer(), 1)

	r.a.Began(1)
	r.clock.pass(defaults.unplaced + 100*time.Millisecond)
	r.a.reconcile()

	if !r.scr.hidden {
		t.Fatal("the avatar stayed up under an assistant that has none")
	}
}

// A board with one decoder to spare cannot open the second stream beside the first; the first
// then closes and the next avatar comes up on its next keyframe, never no avatar at all.
func TestABoardWithOneDecoderStillHandsOver(t *testing.T) {
	r, sylvia, orion, opened := twoAvatars(t)
	heldOnScreen(t, r, sylvia, 1)
	box := display.Rect{W: 10, H: 10}
	r.a.Began(1)
	r.a.Showing(1)
	(*opened)[1].blockedBy = (*opened)[0]
	keys := orion.keys.Load()

	orion.show(rtc.Frame{Key: true})
	if !(*opened)[0].closed {
		t.Fatal("the first stream stayed open though the second could not open beside it")
	}
	// The refused stream is no use; the next frame the look is wanted makes a new one.
	r.a.Showing(1)
	if orion.keys.Load() == keys {
		t.Error("asked for no keyframe after making room")
	}
	orion.show(rtc.Frame{Key: true})

	if !r.a.Place(1, box, false) {
		t.Fatal("the next avatar never came up")
	}
	if last := (*opened)[len(*opened)-1]; len(*opened) != 3 || last.slot != 1 || last.hidden {
		t.Fatalf("%d streams, the next avatar's new one not up", len(*opened))
	}
}

// A lone avatar whose decoder broke before its first frame has no other to make room for it. The
// dead stream must still go, so a later showing opens a new one rather than staying audio only.
func TestRegressionALoneAvatarRefusedOnceGetsANewStream(t *testing.T) {
	r := newRig(t, always())
	var opened []*fakeScreen
	r.a.screen = func(slot int) screen {
		s := &fakeScreen{slot: slot}
		opened = append(opened, s)
		return s
	}
	r.connect(t)
	sylvia := r.peer()
	r.a.Showing(0)
	if len(opened) != 1 {
		t.Fatalf("%d streams after the first showing, want 1", len(opened))
	}
	opened[0].failed = surface.Broken

	sylvia.show(rtc.Frame{Key: true})
	if !opened[0].closed {
		t.Fatal("the refused stream stayed open")
	}
	r.a.Showing(0)
	if len(opened) != 1 {
		t.Fatal("a new stream opened at once, with no pause after the refusal")
	}

	r.clock.pass(defaults.retry)
	keys := sylvia.keys.Load()
	r.a.Showing(0)
	if len(opened) != 2 {
		t.Fatalf("%d streams after the pause, want a new one", len(opened))
	}
	if sylvia.keys.Load() == keys {
		t.Error("asked for no keyframe for the new stream")
	}
	sylvia.show(rtc.Frame{Key: true})
	if !r.a.Place(0, display.Rect{W: 10, H: 10}, false) {
		t.Fatal("the avatar never came up on the new stream")
	}
}

// Each refusal in a row waits longer before the next stream, up to the longest retry.
func TestRepeatedRefusalsBackOff(t *testing.T) {
	r := newRig(t, always())
	var opened []*fakeScreen
	r.a.screen = func(slot int) screen {
		// Blocked by a decoder that never closes, each breaks on its first frame.
		s := &fakeScreen{slot: slot, blockedBy: &fakeScreen{}}
		opened = append(opened, s)
		return s
	}
	r.connect(t)
	sylvia := r.peer()
	r.a.Showing(0)
	sylvia.show(rtc.Frame{Key: true})

	r.clock.pass(defaults.retry)
	r.a.Showing(0)
	sylvia.show(rtc.Frame{Key: true})
	r.clock.pass(defaults.retry)
	r.a.Showing(0)

	if len(opened) != 2 {
		t.Fatalf("%d streams, want the second refusal to wait longer than the first", len(opened))
	}
	r.clock.pass(defaults.retry)
	r.a.Showing(0)
	if len(opened) != 3 {
		t.Fatalf("%d streams, want a third once the longer pause is over", len(opened))
	}
}

// A decoder that is slow to start turns samples away for a moment. That is not a refusal, and
// must not cost the avatar still on screen its picture.
func TestASlowSecondDecoderDoesNotCloseTheFirst(t *testing.T) {
	r, sylvia, orion, opened := twoAvatars(t)
	heldOnScreen(t, r, sylvia, 1)
	r.a.Began(1)
	r.a.Showing(1)
	(*opened)[1].slow = 2

	orion.show(rtc.Frame{Key: true})
	orion.show(rtc.Frame{Key: true})

	if (*opened)[0].closed || (*opened)[0].hidden {
		t.Fatal("the avatar on screen was closed for a decoder that was only slow to start")
	}
	orion.show(rtc.Frame{Key: true})
	if !r.a.Place(1, display.Rect{W: 10, H: 10}, false) {
		t.Fatal("the next avatar never came up")
	}
}

// A held picture is placed only about once a second, so when the same assistant speaks again its
// picture must not count as abandoned in the moment between the hold ending and the show
// placing it.
func TestTheSameAssistantSpeakingAgainKeepsItsPicture(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	heldOnScreen(t, r, r.peer(), 0)
	r.clock.pass(900 * time.Millisecond)
	r.peer().show(rtc.Frame{})

	r.a.Began(0)
	r.a.reconcile()

	if r.scr.hidden || !r.a.Showing(0) {
		t.Fatal("the picture went between the hold ending and its own show placing it")
	}
}

// An avatar taken down for another gets a new stream when it shows again mid-reply, and its
// lip-sync delay with it.
func TestTheLipSyncDelayFollowsTheAvatarBackOnScreen(t *testing.T) {
	r, sylvia, orion, opened := twoAvatars(t)
	box := display.Rect{W: 10, H: 10}
	sp := newSpeech(&lane{queued: 16000 / 2}, nil, r.clock.now)
	l := r.a.links[0]
	r.a.Showing(0)
	sylvia.show(rtc.Frame{Key: true})
	r.a.Place(0, box, false)
	r.a.sync(l, sp)
	want := (*opened)[0].delay
	if want == 0 {
		t.Fatal("no delay set")
	}

	r.a.Showing(1)
	orion.show(rtc.Frame{Key: true})
	r.a.Place(1, box, false)
	r.a.Showing(0)
	r.a.sync(l, sp)

	last := (*opened)[len(*opened)-1]
	if last.slot != 0 || last.delay != want {
		t.Errorf("slot %d delay %s after coming back, want slot 0 at %s",
			last.slot, last.delay, want)
	}
}

// The decoder's shown count restarts when the layer reopens for a new picture size or a broken
// decoder; that must not keep the picture from counting as up.
func TestAPictureCountsAfterTheLayerReopens(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	r.scr.shown = 50
	r.a.Showing(0)
	r.scr.shown = 0

	r.peer().show(rtc.Frame{Key: true})

	if !r.a.Showing(0) {
		t.Fatal("the picture does not count after the layer reopened")
	}
}

func TestOtherVideoTakesTheScreen(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	r.busy.Store(true)

	if r.a.Place(0, display.Rect{W: 10, H: 10}, true) {
		t.Fatal("placed while another video has the screen")
	}
}

func TestSpeakingWithoutAConnectionLetsTheCallerPlay(t *testing.T) {
	cfg := always()
	cfg.On = false
	r := newRig(t, cfg)

	handled, _, err := r.a.Speak(context.Background(), 0, r.url+"/clip.mp3", &lane{}, nil)

	if handled || err != nil {
		t.Fatalf("handled %v, err %v", handled, err)
	}
}

// realTime runs Speak's polling against the wall clock with short timings.
func realTime(r *rig) {
	r.a.now = time.Now
	r.a.timing.poll = 2 * time.Millisecond
	r.a.timing.quiet = 30 * time.Millisecond
	r.a.timing.firstSound = 300 * time.Millisecond
	r.a.timing.silent = 2 * time.Second
	r.a.timing.waitLink = 200 * time.Millisecond
	r.a.timing.fedFirstSound = 300 * time.Millisecond
	r.a.timing.fedSilent = 150 * time.Millisecond
}

func TestAReplyIsSpokenByTheAvatarAndEndsAfterItGoesQuiet(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	realTime(r)
	p := r.peer()
	sink := &lane{}
	started := make(chan struct{})

	done := make(chan bool)
	go func() {
		handled, _, err := r.a.Speak(context.Background(), 0, r.url+"/clip.mp3", sink, func() { close(started) })
		if err != nil {
			t.Error(err)
		}
		done <- handled
	}()
	select {
	case <-r.uploaded:
	case <-done:
		t.Fatal("Speak returned before uploading")
	}
	if p.deaf.Load() {
		t.Fatal("the reply's audio is not decoded")
	}
	for range 20 {
		p.play(chunk(4000))
	}
	<-started

	if !<-done {
		t.Fatal("the avatar's reply was not handled")
	}
	if string(r.lt.spoken[0]) != "ID3 clip" {
		t.Errorf("uploaded %q", r.lt.spoken[0])
	}
	if sink.voice == 0 {
		t.Error("none of the stream's audio reached the voice lane")
	}
	if !p.deaf.Load() {
		t.Error("the stream's audio is still decoded after the reply")
	}
}

func TestAnAvatarThatStaysSilentHandsTheReplyBack(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	realTime(r)

	handled, _, err := r.a.Speak(context.Background(), 0, r.url+"/clip.mp3", &lane{}, nil)

	if handled || err != nil {
		t.Fatalf("handled %v, err %v", handled, err)
	}
	if r.lt.interrupted != 1 {
		t.Errorf("interrupted %d times", r.lt.interrupted)
	}
}

func TestStoppingAReplyInterruptsTheAvatar(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	realTime(r)
	r.lt.speaking = true
	ctx, cancel := context.WithCancel(context.Background())
	sink := &lane{}

	go func() {
		<-r.uploaded
		r.peer().play(chunk(4000))
		cancel()
	}()
	handled, _, err := r.a.Speak(ctx, 0, r.url+"/clip.mp3", sink, nil)

	if !handled || err != nil {
		t.Fatalf("handled %v, err %v", handled, err)
	}
	if r.lt.interrupted != 1 || !sink.drained {
		t.Errorf("interrupted %d times, drained %v", r.lt.interrupted, sink.drained)
	}
}

func TestSpeakWaitsForASessionThatIsConnecting(t *testing.T) {
	r := newRig(t, woken())
	realTime(r)
	r.a.Wake(0)
	r.a.reconcile()
	type result struct {
		gen uint64
		ok  bool
	}
	got := make(chan result)
	go func() {
		_, gen, ok := r.a.connected(context.Background(), r.a.links[0])
		got <- result{gen, ok}
	}()
	r.peer().report(webrtc.PeerConnectionStateConnected)
	res := <-got
	gen, ok := res.gen, res.ok

	if !ok || gen == 0 {
		t.Fatal("gave up on a session that was connecting")
	}
}

func TestTheEntitiesUseTheAgreedObjectIDs(t *testing.T) {
	r := newRig(t, schema.Avatar{})
	want := []string{
		"avatar_mode_2", "avatar_url_2", "avatar_id_2",
		"avatar_connection_2", "avatar_session_2", "avatar_state_2", "avatar_fed_2",
	}
	// Home Assistant integrations find these by display name, so they stay English literals.
	names := []string{
		"Avatar mode", "Avatar URL", "Avatar ID",
		"Avatar connection", "Avatar session", "Avatar state", "Avatar fed by Home Assistant",
	}
	got := r.a.Entities(1)
	if len(got) != len(want) {
		t.Fatalf("%d entities, want %d", len(got), len(want))
	}
	for i, e := range got {
		if b := baseOf(e); b.ObjectID != want[i] || b.Name != names[i] {
			t.Errorf("entity %d is %q named %q, want %q named %q", i, b.ObjectID, b.Name, want[i], names[i])
		}
	}
	if got := r.a.ents[0].connection.Options; len(got) != 2 || got[0] != "Always connected" ||
		got[1] != "Connect when woken" {
		t.Errorf("connection options %q", got)
	}
}

// ha-vam feeds a session only while this switch is on, so it must be saved and shown again.
func TestTheFedSwitchIsSavedAndRestored(t *testing.T) {
	r := newRig(t, always())

	r.a.ents[0].fed.OnCommand(true)

	if !r.m.Read().Slot(0).Avatar.Fed {
		t.Fatal("the fed switch was not saved")
	}
	if !r.a.ents[0].fed.Get() {
		t.Error("the fed switch does not show it is on")
	}
	r.a.ents[0].fed.Set(false)
	r.a.Restore(r.m.Read().Words)
	if !r.a.ents[0].fed.Get() {
		t.Error("the saved fed switch was not restored")
	}
}

func TestAnInvalidURLFromHomeAssistantIsNotSaved(t *testing.T) {
	r := newRig(t, schema.Avatar{})

	r.a.ents[0].url.OnCommand("http://avatar.lan/")
	r.a.ents[0].connection.OnCommand("Always connected")

	got := r.m.Read().Slot(0).Avatar
	if got.URL != "" || got.Connection != schema.AvatarAlways {
		t.Fatalf("saved %+v", got)
	}
}

func TestNamingTheNextAnnouncementsAssistantConnectsItsAvatar(t *testing.T) {
	r := newRig(t, woken())

	r.a.Chosen(0, true)
	r.connect(t)
	r.a.Chosen(0, false)
	r.clock.pass(defaults.linger + time.Second)
	r.a.reconcile()

	if !r.peer().closed.Load() {
		t.Fatal("the session stayed open after the choice was released")
	}
}

// Sound left over on the stream is not the reply: until LiveTalking says it is speaking the clip,
// a quiet moment does not end it, or a line would count as spoken before it began.
func TestAReplyIsNotOverBeforeLiveTalkingStartsIt(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	realTime(r)
	r.a.timing.firstSound = 2 * time.Second
	p := r.peer()

	done := make(chan bool, 1)
	go func() {
		handled, _, _ := r.a.Speak(context.Background(), 0, r.url+"/clip.mp3", &lane{}, nil)
		done <- handled
	}()
	<-r.uploaded
	p.play(chunk(4000))
	select {
	case <-done:
		t.Fatal("the reply ended on leftover sound, before LiveTalking began speaking")
	case <-time.After(10 * r.a.timing.quiet):
	}

	r.lt.mu.Lock()
	r.lt.speaking = true
	r.lt.mu.Unlock()
	for range 10 {
		p.play(chunk(4000))
		time.Sleep(5 * time.Millisecond)
	}
	r.lt.mu.Lock()
	r.lt.speaking = false
	r.lt.mu.Unlock()

	if !<-done {
		t.Fatal("the reply was not handled")
	}
}

// An avatar readied but never shown still holds a decoder; it must not keep the other from
// opening its own on a board with one to spare.
func TestAnAvatarReadiedButNeverShownMakesRoomToo(t *testing.T) {
	r, sylvia, orion, opened := twoAvatars(t)
	r.a.Showing(1)
	orion.show(rtc.Frame{Key: true})
	r.a.Showing(0)
	(*opened)[0].blockedBy = (*opened)[1]

	sylvia.show(rtc.Frame{Key: true})
	r.a.Showing(0)
	sylvia.show(rtc.Frame{Key: true})

	if !r.a.Place(0, display.Rect{W: 10, H: 10}, false) {
		t.Fatal("the avatar never came up behind one that was only readied")
	}
}

package avatar

import (
	"context"
	"encoding/json"
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
}

func (s *fakeScreen) Show(rtc.Frame) { s.mu.Lock(); s.shown++; s.mu.Unlock() }
func (s *fakeScreen) Place(r display.Rect) {
	s.mu.Lock()
	s.placed, s.hidden = r, false
	s.mu.Unlock()
}
func (s *fakeScreen) Hide()                 { s.mu.Lock(); s.hidden = true; s.mu.Unlock() }
func (s *fakeScreen) Delay(d time.Duration) { s.mu.Lock(); s.delay = d; s.mu.Unlock() }
func (s *fakeScreen) Refused() bool         { return s.refused }
func (s *fakeScreen) Stats() call.LayerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return call.LayerStats{Shown: s.shown}
}
func (s *fakeScreen) Close() {}

// liveTalking is a fake LiveTalking server.
type liveTalking struct {
	mu          sync.Mutex
	offers      int
	avatar      string
	spoken      [][]byte
	interrupted int
	speaking    bool
	uploaded    chan struct{}
}

func (lt *liveTalking) handler(w http.ResponseWriter, r *http.Request) {
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
		if lt.speaking {
			_, _ = io.WriteString(w, `{"code":0,"data":true}`)
		} else {
			_, _ = io.WriteString(w, `{"code":0,"data":false}`)
		}
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
	r.a.screen = func() screen { return r.scr }
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
		t.Fatal("fed the decoder before it was staged")
	}
	if r.a.Place(0, box) {
		t.Fatal("placed with no picture")
	}
	if p.keys.Load() == 0 {
		t.Error("asked for no keyframe when staged")
	}
	p.show(rtc.Frame{})
	if r.scr.shown != 0 {
		t.Fatal("fed a delta frame before a keyframe")
	}
	p.show(rtc.Frame{Key: true})
	p.show(rtc.Frame{})

	if !r.a.Place(0, box) || r.scr.shown != 2 || r.scr.placed != box {
		t.Fatalf("shown %d at %+v", r.scr.shown, r.scr.placed)
	}
}

func TestAnUnplacedAvatarHidesAndStopsFeeding(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	r.a.Place(0, display.Rect{W: 10, H: 10})
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
	r.a.Place(0, box)
	r.peer().show(rtc.Frame{Key: true})

	for range 30 {
		r.clock.pass(100 * time.Millisecond)
		r.a.Place(0, box)
		r.a.reconcile()
	}
	if r.a.Place(0, box) || !r.scr.hidden {
		t.Fatal("a stalled picture stays up")
	}

	r.a.Wake(0)
	r.a.Place(0, box)
	r.peer().show(rtc.Frame{Key: true})
	if !r.a.Place(0, box) {
		t.Fatal("the picture did not come back on the next wake")
	}
}

func TestOtherVideoTakesTheScreen(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	r.busy.Store(true)

	if r.a.Place(0, display.Rect{W: 10, H: 10}) {
		t.Fatal("placed while another video has the screen")
	}
}

func TestSpeakingWithoutAConnectionLetsTheCallerPlay(t *testing.T) {
	cfg := always()
	cfg.On = false
	r := newRig(t, cfg)

	handled, err := r.a.Speak(context.Background(), 0, r.url+"/clip.mp3", &lane{}, nil)

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
		handled, err := r.a.Speak(context.Background(), 0, r.url+"/clip.mp3", sink, func() { close(started) })
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

	handled, err := r.a.Speak(context.Background(), 0, r.url+"/clip.mp3", &lane{}, nil)

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
	handled, err := r.a.Speak(ctx, 0, r.url+"/clip.mp3", sink, nil)

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
		"avatar_connection_2", "avatar_session_2", "avatar_state_2",
	}
	for i, e := range r.a.Entities(1) {
		if b := baseOf(e); b.ObjectID != want[i] {
			t.Errorf("entity %d is %q, want %q", i, b.ObjectID, want[i])
		}
	}
	if got := r.a.ents[0].connection.Options; len(got) != 2 || got[0] != "Always connected" ||
		got[1] != "Connect when woken" {
		t.Errorf("connection options %q", got)
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

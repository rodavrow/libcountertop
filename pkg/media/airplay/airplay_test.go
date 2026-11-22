package airplay

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"math"
	"os"
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/media/urlplayer"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

func TestMessagesRoundTrip(t *testing.T) {
	var b bytes.Buffer
	sent := frameMessage(1500, 1066, 800, 0, 0, 0, 1, 0x65)
	if err := writeMessage(&b, sent.kind, sent.payload); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	m, err := readMessage(&b)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	f, err := frameOf(m.payload)
	if err != nil {
		t.Fatalf("frameOf: %v", err)
	}
	if m.kind != msgFrame || f.at != 1500*time.Microsecond || f.width != 1066 || f.height != 800 ||
		!bytes.Equal(f.data, []byte{0, 0, 0, 1, 0x65}) {
		t.Errorf(
			"got kind %q at %v size %dx%d data %x",
			rune(m.kind),
			f.at,
			f.width,
			f.height,
			f.data,
		)
	}
}

func TestAnOversizedMessageIsRefused(t *testing.T) {
	head := binary.LittleEndian.AppendUint32(nil, msgFrame)
	head = binary.LittleEndian.AppendUint32(head, maxPayload+1)
	if _, err := readMessage(bytes.NewReader(head)); err == nil {
		t.Error("an oversized message was read")
	}
}

func readyBytes(raop, air string, raopTXT, airTXT []byte) []byte {
	p := binary.LittleEndian.AppendUint32(nil, version)
	p = binary.LittleEndian.AppendUint32(p, 7000)
	for _, n := range []int{len(raop), len(air), len(raopTXT), len(airTXT)} {
		p = binary.LittleEndian.AppendUint32(p, uint32(n))
	}
	p = append(p, raop...)
	p = append(p, air...)
	p = append(p, raopTXT...)
	return append(p, airTXT...)
}

func TestReadyCarriesWhatToAnnounce(t *testing.T) {
	raopTXT := []byte("\x05ch=02\x09pk=abcdef")
	airTXT := []byte("\x14deviceid=AA:BB:CC:DD")
	rd, err := readyOf(readyBytes(
		"AABBCCDDEEFF@Kitchen._raop._tcp.local", "Kitchen._airplay._tcp.local", raopTXT, airTXT,
	))
	if err != nil {
		t.Fatal(err)
	}
	if rd.port != 7000 || rd.raop.instance != "AABBCCDDEEFF@Kitchen" ||
		rd.airplay.instance != "Kitchen" {
		t.Errorf("ready %+v", rd)
	}
	if len(rd.raop.text) != 2 || rd.raop.text[0] != "ch=02" || rd.raop.text[1] != "pk=abcdef" {
		t.Errorf("raop text %q", rd.raop.text)
	}
	if len(rd.airplay.text) != 1 || rd.airplay.text[0] != "deviceid=AA:BB:CC:DD" {
		t.Errorf("airplay text %q", rd.airplay.text)
	}
	bad := readyBytes("x._raop._tcp.local", "x._airplay._tcp.local", []byte("\x09ch=02"), nil)
	if _, err := readyOf(bad); err == nil {
		t.Error("a TXT entry longer than its record was read")
	}
	if _, err := readyOf(bad[:len(bad)-1]); err == nil {
		t.Error("a short ready was read")
	}
}

func TestSilenceKeepsToTheClock(t *testing.T) {
	m := newMirror("mac", mirroring{}, 48000, 2, func() {})
	pcm := make([]int16, 48000*2)
	for i := range pcm {
		pcm[i] = 1
	}
	n, err := m.Read(context.Background(), pcm)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	lead := int(silenceLead * 48000 / time.Second * 2)
	if n < lead || n > lead+2*480 {
		t.Errorf("first read gave %d samples, want about %d", n, lead)
	}
	for _, v := range pcm[:n] {
		if v != 0 {
			t.Fatal("the silence was not silent")
		}
	}
	if n, _ := m.Read(context.Background(), pcm); n > 2*4800 {
		t.Errorf("an immediate second read gave %d samples", n)
	}

	m.end()
	if _, err := m.Read(context.Background(), pcm); !errors.Is(err, io.EOF) {
		t.Errorf("Read after the end = %v, want EOF", err)
	}
}

func TestFramesCountFromTheFirst(t *testing.T) {
	m := newMirror(
		"mac",
		mirroring{codec: codecH264, width: 1920, height: 1080},
		48000,
		2,
		func() {},
	)
	ctx := context.Background()
	m.push(ctx, frame{at: 5 * time.Second, data: []byte{1}})
	m.push(ctx, frame{at: 5*time.Second + 33*time.Millisecond, data: []byte{2}})

	p, err := m.Live(ctx)
	if err != nil || p.Codec != playback.H264 || p.Width != 1920 || p.Height != 1080 {
		t.Fatalf("Live = %+v, %v", p, err)
	}
	for _, want := range []time.Duration{0, 33 * time.Millisecond} {
		_, at, err := p.Next(ctx)
		if err != nil || at != want {
			t.Errorf("frame at %v (%v), want %v", at, err, want)
		}
	}
	m.end()
	if _, _, err := p.Next(ctx); !errors.Is(err, io.EOF) {
		t.Errorf("Next after the end = %v, want EOF", err)
	}
}

type output struct {
	playback.Output
	played, stopped []playback.Source
	ended           func()
	width, height   int
	restarts        int
}

func (o *output) Play(src playback.Source) { o.played = append(o.played, src) }
func (o *output) Stop(src playback.Source) { o.stopped = append(o.stopped, src) }

func screeningRig(t *testing.T) (*screening, *output, *os.File) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	t.Cleanup(func() { read.Close(); write.Close() })
	out := &output{width: 1200, height: 1920}
	r := &Receiver{opts: Options{
		Settings: &section{cfg: schema.DefaultAirPlay()},
		Rate:     48000,
		Channels: 2,
		Size:     func() (int, int) { return out.width, out.height },
		Output: func(ended func()) playback.Output {
			out.ended = ended
			return out
		},
	}}
	s := &screening{
		r:       r,
		conn:    write,
		width:   1200,
		height:  1920,
		restart: func() { out.restarts++ },
	}
	return s, out, read
}

func mirrorMessage(w, h uint32) message { return codecMirrorMessage(codecH264, w, h) }

func frameMessage(us uint64, w, h uint32, data ...byte) message {
	p := binary.LittleEndian.AppendUint64(nil, us)
	p = binary.LittleEndian.AppendUint32(p, w)
	p = binary.LittleEndian.AppendUint32(p, h)
	return message{kind: msgFrame, payload: append(p, data...)}
}

func codecMirrorMessage(codec, w, h uint32) message {
	p := binary.LittleEndian.AppendUint32(nil, codec)
	p = binary.LittleEndian.AppendUint32(p, w)
	p = binary.LittleEndian.AppendUint32(p, h)
	return message{kind: msgMirror, payload: p}
}

func TestAMirrorShowsTheCodecTheSenderChose(t *testing.T) {
	for _, c := range []struct {
		codec uint32
		want  uint32
	}{
		{codecH264, playback.H264},
		{codecH265, playback.HEVC},
	} {
		s, out, _ := screeningRig(t)
		ctx := context.Background()
		s.handle(ctx, codecMirrorMessage(c.codec, 1920, 1080))
		if len(out.played) != 1 {
			t.Fatalf("codec %d: played %d sources, want 1", c.codec, len(out.played))
		}
		p, _ := out.played[0].(playback.Live).Live(ctx)
		if p.Codec != c.want {
			t.Errorf("codec %d: picture %08x, want %08x", c.codec, p.Codec, c.want)
		}
	}
}

func TestAMirrorInAnUnknownCodecIsRefused(t *testing.T) {
	s, out, helper := screeningRig(t)
	s.handle(context.Background(), codecMirrorMessage(9, 1920, 1080))
	if len(out.played) != 0 {
		t.Errorf("played %d sources, want none", len(out.played))
	}
	m, err := readMessage(helper)
	if err != nil || m.kind != msgDisconnect {
		t.Fatalf("helper heard %q (%v), want a disconnect", rune(m.kind), err)
	}
}

func TestAMirrorPlaysUntilTheSenderEndsIt(t *testing.T) {
	s, out, _ := screeningRig(t)
	ctx := context.Background()
	s.handle(ctx, message{kind: msgClient, payload: []byte("Office Mac")})
	s.handle(ctx, mirrorMessage(1920, 1080))
	if len(out.played) != 1 {
		t.Fatalf("played %d sources, want 1", len(out.played))
	}
	src := out.played[0]
	if got := src.Showing().Title; got != "Office Mac" {
		t.Errorf("showing %q", got)
	}

	s.handle(ctx, frameMessage(7, 1920, 1080))
	p, _ := src.(playback.Live).Live(ctx)
	if _, at, err := p.Next(ctx); err != nil || at != 0 {
		t.Errorf("first frame at %v (%v)", at, err)
	}

	s.handle(ctx, frameMessage(40007, 1066, 800))
	if _, _, err := p.Next(ctx); err != nil {
		t.Fatalf("second frame: %v", err)
	}
	if w, h := p.Size(); w != 1066 || h != 800 {
		t.Errorf("after the sender resized, size %dx%d, want 1066x800", w, h)
	}

	s.handle(ctx, message{kind: msgEnded})
	if len(out.stopped) != 1 || out.stopped[0] != src {
		t.Errorf("stopped %v, want the mirror", out.stopped)
	}
	if _, _, err := p.Next(ctx); !errors.Is(err, io.EOF) {
		t.Errorf("Next after the end = %v, want EOF", err)
	}
}

func TestAudioOnlyPlaysWithoutAPicture(t *testing.T) {
	s, out, _ := screeningRig(t)
	ctx := context.Background()
	s.handle(ctx, message{kind: msgClient, payload: []byte("Office Mac")})
	s.handle(ctx, message{kind: msgSound})
	if len(out.played) != 1 {
		t.Fatalf("played %d sources, want 1", len(out.played))
	}
	src := out.played[0]
	if _, ok := src.(playback.Pictures); ok {
		t.Error("an audio-only session offers a picture")
	}

	s.handle(ctx, audioMessage(0))
	pcm := make([]int16, 8)
	if n, err := src.Read(ctx, pcm); err != nil || n != 4 || pcm[0] != 1000 {
		t.Errorf("Read = %d %v (%v), want the 4 samples sent", n, pcm[:n], err)
	}

	s.handle(ctx, message{kind: msgEnded})
	if len(out.stopped) != 1 || out.stopped[0] != src {
		t.Errorf("stopped %v, want the audio", out.stopped)
	}
}

type section struct{ cfg schema.AirPlay }

func (s *section) Read() schema.AirPlay { return s.cfg }

func (s *section) Update(change func(*schema.AirPlay)) error {
	change(&s.cfg)
	return nil
}

func audioMessage(due time.Duration) message {
	p := binary.LittleEndian.AppendUint32(nil, 48000)
	p = binary.LittleEndian.AppendUint32(p, 2)
	p = binary.LittleEndian.AppendUint32(p, uint32(int32(due/time.Microsecond)))
	for range 4 {
		p = binary.LittleEndian.AppendUint16(p, 1000)
	}
	return message{kind: msgAudio, payload: p}
}

func TestSyncedAudioWaitsForItsTime(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		mode    schema.AirPlayAudioMode
		latency time.Duration
		want    int
	}{
		{schema.AirPlaySynced, 0, 0},
		{schema.AirPlaySynced, time.Second, 4},
		{schema.AirPlayInstant, 0, 4},
	} {
		s := newSound("mac", 48000, 2, tc.mode, func() {})
		s.latency = tc.latency
		a, err := audioOf(audioMessage(time.Second).payload)
		if err != nil {
			t.Fatal(err)
		}
		s.hear(a)
		pcm := make([]int16, 8)
		if n, err := s.Read(ctx, pcm); err != nil || n != tc.want {
			t.Errorf("%s %v: Read = %d (%v), want %d", tc.mode, tc.latency, n, err, tc.want)
		}
	}
}

func TestWithoutAScreenAMirrorIsRefused(t *testing.T) {
	s, out, helper := screeningRig(t)
	s.r.opts.Size = nil
	s.handle(context.Background(), mirrorMessage(1920, 1080))
	if len(out.played) != 0 {
		t.Errorf("played %d sources, want none", len(out.played))
	}
	m, err := readMessage(helper)
	if err != nil || m.kind != msgDisconnect {
		t.Fatalf("helper heard %q (%v), want a disconnect", rune(m.kind), err)
	}
}

func TestClosingThePictureDisconnectsTheSender(t *testing.T) {
	s, out, helper := screeningRig(t)
	ctx := context.Background()
	s.handle(ctx, mirrorMessage(1280, 720))
	src := out.played[0]

	out.ended()
	m, err := readMessage(helper)
	if err != nil || m.kind != msgDisconnect {
		t.Fatalf("helper heard %q (%v), want a disconnect", rune(m.kind), err)
	}
	pcm := make([]int16, 960)
	if _, err := src.Read(ctx, pcm); !errors.Is(err, io.EOF) {
		t.Errorf("Read after the picture closed = %v, want EOF", err)
	}
	s.handle(ctx, frameMessage(7, 1920, 1080))
}

func TestHeardAudioPlaysBeforeSilence(t *testing.T) {
	m := newMirror("mac", mirroring{}, 48000, 2, func() {})
	m.hear(audio{rate: 48000, channels: 1, pcm: []int16{7, 8, 9}})
	pcm := make([]int16, 960)
	n, err := m.Read(context.Background(), pcm)
	if err != nil || n != 6 || pcm[0] != 7 || pcm[1] != 7 || pcm[5] != 9 {
		t.Fatalf("read %d samples %v (%v), want the heard audio in stereo", n, pcm[:n], err)
	}
	if n, _ := m.Read(context.Background(), pcm); n != 0 {
		t.Errorf("a short gap in the audio filled %d samples of silence", n)
	}

	m.hear(audio{rate: 48000, channels: 2, pcm: make([]int16, 48000*2)})
	if most := int(mostAudio * 48000 / time.Second * 2); m.queued != most {
		t.Errorf("held %d samples, want at most %d", m.queued, most)
	}
}

func TestATurnRestartsTheReceiverOnceNobodyIsMirroring(t *testing.T) {
	s, out, _ := screeningRig(t)
	ctx := context.Background()
	s.handle(ctx, mirrorMessage(1200, 1920))

	out.width, out.height = 1920, 1200
	s.resized()
	if out.restarts != 0 {
		t.Fatal("a turn restarted the receiver mid-mirror")
	}
	s.handle(ctx, message{kind: msgEnded})
	if out.restarts != 1 {
		t.Errorf("restarted %d times after the mirror ended, want 1", out.restarts)
	}

	s.width, s.height = 1920, 1200
	s.resized()
	if out.restarts != 1 {
		t.Error("an unchanged size restarted the receiver")
	}
}

func TestTheScreenShrinksIntoWhatTheDecoderTakes(t *testing.T) {
	most := image.Pt(1920, 1088)
	for _, c := range []struct{ w, h, wantW, wantH int }{
		{1200, 1920, 1088, 1740},
		{1920, 1200, 1740, 1088},
		{1280, 800, 1280, 800},
		{800, 1280, 800, 1280},
	} {
		if w, h := fit(c.w, c.h, most); w != c.wantW || h != c.wantH {
			t.Errorf("fit(%dx%d) = %dx%d, want %dx%d", c.w, c.h, w, h, c.wantW, c.wantH)
		}
	}
}

func TestAMirrorSendsFramesOnlyOnChange(t *testing.T) {
	m := newMirror("mac", mirroring{codec: codecH264}, 48000, 2, func() {})
	if p, _ := m.Live(context.Background()); !p.Sparse {
		t.Error("the mirror's picture is not marked sparse")
	}
}

func playMessage(at time.Duration, location string) message {
	p := binary.LittleEndian.AppendUint64(nil, uint64(at.Microseconds()))
	return message{kind: msgPlay, payload: append(p, location...)}
}

func TestVideoMessagesAreRead(t *testing.T) {
	p, err := playOf(playMessage(90*time.Second, "http://localhost:7000/master.m3u8").payload)
	if err != nil || p.at != 90*time.Second || p.location != "http://localhost:7000/master.m3u8" {
		t.Errorf("play %+v (%v)", p, err)
	}
	if _, err := playOf(binary.LittleEndian.AppendUint64(nil, 0)); err == nil {
		t.Error("a play without a location was read")
	}
	if at, err := scrubOf(binary.LittleEndian.AppendUint64(nil, 2500000)); err != nil ||
		at != 2500*time.Millisecond {
		t.Errorf("scrub %v (%v)", at, err)
	}
	if playing, err := rateOf(binary.LittleEndian.AppendUint32(nil, 0)); err != nil || playing {
		t.Errorf("rate 0 read as playing=%v (%v)", playing, err)
	}
}

func TestThePositionIsWrittenAsTheHelperReadsIt(t *testing.T) {
	b := position{at: 1500 * time.Millisecond, length: time.Minute, state: statePaused}.bytes()
	if len(b) != 24 {
		t.Fatalf("%d bytes, want 24", len(b))
	}
	at := int64(binary.LittleEndian.Uint64(b[0:]))
	length := int64(binary.LittleEndian.Uint64(b[8:]))
	st := int64(binary.LittleEndian.Uint64(b[16:]))
	if at != 1500000 || length != 60000000 || st != int64(statePaused) {
		t.Errorf("at %d length %d state %d", at, length, st)
	}
}

func TestAVideoThatCannotLoadTellsTheSenderItIsOver(t *testing.T) {
	s, _, helper := screeningRig(t)
	s.handle(context.Background(), playMessage(0, "mlhls://localhost/master.m3u8"))
	m, err := readMessage(helper)
	if err != nil || m.kind != msgPosition {
		t.Fatalf("helper heard %q (%v), want a position", rune(m.kind), err)
	}
	if st := state(binary.LittleEndian.Uint64(m.payload[16:])); st != stateOver {
		t.Errorf("state %d, want over", st)
	}
	if s.streaming() != nil {
		t.Error("a refused video is still held")
	}
}

func TestAVideoThatPlaysOutEndsWithoutEndingTheSession(t *testing.T) {
	v := &video{player: urlplayer.New(urlplayer.Options{})}
	if st := v.position().state; st != stateLoading {
		t.Fatalf("state %d before anything played, want loading", st)
	}
	v.end.CompareAndSwap(0, int64(stateEnded))
	if st := v.position().state; st != stateEnded {
		t.Errorf("state %d after the stream ran out, want ended", st)
	}
	v.end.Store(int64(stateOver))
	if st := v.position().state; st != stateOver {
		t.Errorf("state %d after a stop on this device, want over", st)
	}
}

func TestAClosedConnectionLeavesAVideoPlaying(t *testing.T) {
	s, _, _ := screeningRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	v := &video{player: urlplayer.New(urlplayer.Options{}), cancel: cancel}
	v.workers.Go(func() { <-ctx.Done() })
	s.video = v
	s.handle(context.Background(), message{kind: msgEnded})
	if s.streaming() != v {
		t.Error("a closed connection stopped the video")
	}
}

func TestASendersStopWaitsForItsNextItem(t *testing.T) {
	s, out, _ := screeningRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	v := &video{player: urlplayer.New(urlplayer.Options{Output: out}), cancel: cancel}
	v.workers.Go(func() { <-ctx.Done() })
	s.video = v
	s.handle(context.Background(), message{kind: msgStop})
	if s.streaming() != v {
		t.Error("a stop between items dropped the session's video at once")
	}
}

type loud struct {
	level int
	muted bool
}

func (l *loud) Level() int                           { return l.level }
func (l *loud) SetLevel(level int)                   { l.level = level }
func (l *loud) Muted() bool                          { return l.muted }
func (l *loud) Mute(on bool)                         { l.muted = on }
func (l *loud) Listen(func(level int)) (stop func()) { return func() {} }

func volumeMessage(kind uint32, db float32) message {
	return message{kind: kind, payload: binary.LittleEndian.AppendUint32(nil, math.Float32bits(db))}
}

func TestTheSendersVolumeSetsTheDevice(t *testing.T) {
	s, _, _ := screeningRig(t)
	l := &loud{level: 40}
	s.r.opts.Volume = l
	ctx := context.Background()

	s.handle(ctx, volumeMessage(msgVolume, -15))
	if l.level != 50 || l.muted {
		t.Errorf("-15 dB gave level %d muted %v, want 50 and sounding", l.level, l.muted)
	}
	s.handle(ctx, volumeMessage(msgVolume, mutedDB))
	if !l.muted || l.level != 50 {
		t.Errorf("-144 dB gave level %d muted %v, want muted at 50", l.level, l.muted)
	}
	s.handle(ctx, volumeMessage(msgVolume, 0))
	if l.muted || l.level != 100 {
		t.Errorf("0 dB gave level %d muted %v, want 100 and sounding", l.level, l.muted)
	}
	s.handle(ctx, message{kind: msgMute, payload: binary.LittleEndian.AppendUint32(nil, 1)})
	if !l.muted {
		t.Error("the sender's mute did not mute the device")
	}
}

func TestTheDevicesLevelIsToldInDecibels(t *testing.T) {
	for _, c := range []struct {
		level int
		muted bool
		want  float32
	}{
		{100, false, 0},
		{50, false, -15},
		{0, false, mutedDB},
		{80, true, mutedDB},
	} {
		if got := decibels(c.level, c.muted); got != c.want {
			t.Errorf("decibels(%d, %v) = %v, want %v", c.level, c.muted, got, c.want)
		}
		if !c.muted && c.level > 0 && levelOf(c.want) != c.level {
			t.Errorf("levelOf(%v) = %d, want %d back", c.want, levelOf(c.want), c.level)
		}
	}
}

func TestACodeIsShownUntilTheSenderPairs(t *testing.T) {
	s, _, _ := screeningRig(t)
	var shown []string
	released := 0
	s.r.ShowCodes(func(client, code string) func() {
		shown = append(shown, client+" "+code)
		return func() { released++ }
	})
	ctx := context.Background()
	s.handle(ctx, message{kind: msgClient, payload: []byte("xenu")})
	s.handle(ctx, message{kind: msgPin, payload: []byte("8911")})
	s.handle(ctx, message{kind: msgPin, payload: []byte("7501")})
	if len(shown) != 2 || shown[1] != "xenu 7501" || released != 1 {
		t.Fatalf("shown %v, released %d; want the second code replacing the first", shown, released)
	}
	s.handle(ctx, message{kind: msgPaired, payload: []byte("xenu")})
	if released != 2 {
		t.Errorf("released %d after pairing, want 2", released)
	}
	s.hideCode()
	if released != 2 {
		t.Error("a released code was released again")
	}
}

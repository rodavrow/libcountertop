package videoplay

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/media/playback"
)

const rate = 48000

func wav(seconds int) []byte {
	samples := make([]int16, rate*2*seconds)
	var b bytes.Buffer
	size := uint32(len(samples) * 2)
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, 36+size)
	b.WriteString("WAVEfmt ")
	for _, v := range []any{
		uint32(16), uint16(1), uint16(2), uint32(rate), uint32(rate * 4), uint16(4), uint16(16),
	} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, size)
	_ = binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}

func serve(t *testing.T) string {
	t.Helper()
	body := wav(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type fakeOutput struct {
	mu      sync.Mutex
	log     []string
	sources []playback.Source
	ended   func()
}

func (f *fakeOutput) record(s string) {
	f.mu.Lock()
	f.log = append(f.log, s)
	f.mu.Unlock()
}

func (f *fakeOutput) calls() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.log, " ")
}

func (f *fakeOutput) last() playback.Source {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sources[len(f.sources)-1]
}

func (f *fakeOutput) Attend(*playback.Session) {}
func (f *fakeOutput) Failed(error)             { f.record("failed") }
func (f *fakeOutput) Play(src playback.Source) {
	f.mu.Lock()
	f.sources = append(f.sources, src)
	f.mu.Unlock()
	f.record("play")
}
func (f *fakeOutput) Stop(playback.Source)    { f.record("stop") }
func (f *fakeOutput) Handoff(playback.Source) { f.record("handoff") }
func (f *fakeOutput) Pause(playback.Source)   { f.record("pause") }
func (f *fakeOutput) Resume(playback.Source)  { f.record("resume") }
func (f *fakeOutput) Position(src playback.Source) (time.Duration, bool) {
	return src.Start(), false
}
func (f *fakeOutput) Behind(playback.Source) time.Duration { return 0 }
func (f *fakeOutput) Changed(playback.Source)              {}

func newPlayer() (*Player, *fakeOutput) {
	out := &fakeOutput{}
	p := New(Options{Output: func(ended func()) playback.Output {
		out.ended = ended
		return out
	}})
	return p, out
}

func (f *fakeOutput) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.calls() != want {
		if time.Now().After(deadline) {
			t.Fatalf("the output saw %q, want %q", f.calls(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitFor(t *testing.T, p *Player, want State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for p.State() != want {
		if time.Now().After(deadline) {
			t.Fatalf("the state is %s, want %s", p.State(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := p.state.Get(); got != string(want) {
		t.Fatalf("video_state says %q, want %q", got, want)
	}
}

func playing(t *testing.T) (*Player, *fakeOutput) {
	t.Helper()
	p, out := newPlayer()
	if err := p.Play(context.Background(), serve(t)+"/a.mp4", "Test", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, Playing)
	return p, out
}

func action(t *testing.T, p *Player, name string, args map[string]any) error {
	t.Helper()
	c, err := esphome.NewCall(args)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Actions() {
		if a.Name == name {
			_, err := a.Run(c)
			return err
		}
	}
	t.Fatalf("no action %s", name)
	return nil
}

func TestTheActionsAreTheContract(t *testing.T) {
	p, _ := newPlayer()
	var names []string
	for _, a := range p.Actions() {
		names = append(names, a.Name)
	}
	want := "play_video pause_video resume_video stop_video seek_video"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("actions are %q, want %q", got, want)
	}
}

func TestPlayVideoRefusesBadArguments(t *testing.T) {
	cases := map[string]map[string]any{
		"no url":           {},
		"a public url":     {"url": "https://93.184.215.14/a.mp4"},
		"a file url":       {"url": "file:///etc/passwd"},
		"a negative start": {"url": "http://192.168.1.10/a.mp4", "start": -5},
		"a long title": {
			"url":   "http://192.168.1.10/a.mp4",
			"title": strings.Repeat("x", maxTitle+1),
		},
		"a long url": {"url": "http://192.168.1.10/" + strings.Repeat("a", maxURL)},
	}
	for name, args := range cases {
		p, out := newPlayer()
		if err := action(t, p, "play_video", args); err == nil {
			t.Errorf("%s was accepted", name)
		}
		if p.State() != Idle || out.calls() != "" {
			t.Errorf("%s left the state %s and the output saw %q", name, p.State(), out.calls())
		}
	}
}

func TestAPublicURLIsRefusedAsNotLocal(t *testing.T) {
	p, _ := newPlayer()
	err := action(t, p, "play_video", map[string]any{"url": "https://93.184.215.14/a.mp4"})
	if !errors.Is(err, ErrNotLocal) {
		t.Fatalf("a public url gave %v, want ErrNotLocal", err)
	}
}

func TestPlayVideoReadsItsArguments(t *testing.T) {
	p, out := newPlayer()
	args := map[string]any{"url": serve(t) + "/a.mp4", "title": "Test", "start": 1}
	err := action(t, p, "play_video", args)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, Playing)
	src := out.last()
	if got := src.Showing().Title; got != "Test" {
		t.Errorf("the title shown is %q, want Test", got)
	}
	if got := src.Start(); got != time.Second {
		t.Errorf("the video starts at %v, want 1s", got)
	}
	if got := p.position.Get(); got != 1 {
		t.Errorf("video_position is %v, want 1", got)
	}
}

func TestControlsWithNothingPlayingFail(t *testing.T) {
	p, _ := newPlayer()
	for _, name := range []string{"pause_video", "resume_video"} {
		if err := action(t, p, name, nil); err == nil {
			t.Errorf("%s with nothing playing succeeded", name)
		}
	}
	if err := action(t, p, "seek_video", map[string]any{"seconds": 3}); err == nil {
		t.Error("seek_video with nothing playing succeeded")
	}
	if err := action(t, p, "stop_video", nil); err != nil {
		t.Errorf("stop_video with nothing playing failed: %v", err)
	}
}

func TestPauseResumeSeekAndStop(t *testing.T) {
	p, out := playing(t)
	if err := action(t, p, "pause_video", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, Paused)
	if err := action(t, p, "resume_video", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, Playing)
	if err := action(t, p, "seek_video", map[string]any{"seconds": -1}); err == nil {
		t.Fatal("a negative seek was accepted")
	}
	if err := action(t, p, "seek_video", map[string]any{"seconds": 1}); err != nil {
		t.Fatal(err)
	}
	if got := p.position.Get(); got != 1 {
		t.Errorf("video_position after a seek is %v, want 1", got)
	}
	out.wait(t, "play pause resume handoff play")
	if got := out.last().Start(); got != time.Second {
		t.Errorf("the seeked video starts at %v, want 1s", got)
	}
	if err := action(t, p, "stop_video", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, Idle)
	out.wait(t, "play pause resume handoff play stop")
	if got := p.position.Get(); got != 0 {
		t.Errorf("video_position after stopping is %v, want 0", got)
	}
}

func TestTheScreenControlsDriveThePlayer(t *testing.T) {
	p, out := playing(t)
	src := out.last()
	src.Pause()
	waitFor(t, p, Paused)
	src.Play()
	waitFor(t, p, Playing)
	src.Stop()
	waitFor(t, p, Idle)
}

func TestTheEndOfTheVideoReturnsToIdle(t *testing.T) {
	p, out := playing(t)
	out.last().(playback.Finisher).Finished()
	waitFor(t, p, Idle)
	if !strings.HasSuffix(out.calls(), "stop") {
		t.Errorf("the output was not let go: %q", out.calls())
	}
}

func TestAnotherSourceTakingTheOutputStopsTheVideo(t *testing.T) {
	p, out := playing(t)
	out.last().Stop()
	out.ended()
	waitFor(t, p, Idle)
}

func TestTheOutputEndingStopsTheVideo(t *testing.T) {
	p, out := playing(t)
	out.ended()
	waitFor(t, p, Idle)
	if !strings.HasSuffix(out.calls(), "stop") {
		t.Errorf("the output was not let go: %q", out.calls())
	}
}

func TestAVideoThatWillNotOpenIsAnError(t *testing.T) {
	p, out := newPlayer()
	if err := p.Play(context.Background(), serve(t)+"/missing", "", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, Failed)
	if got := out.calls(); got != "failed stop" {
		t.Errorf("the output saw %q, want the failure and a stop", got)
	}
	if err := action(t, p, "pause_video", nil); err == nil {
		t.Error("a failed video paused")
	}
}

func TestAFailureMidwayIsAnError(t *testing.T) {
	p, out := playing(t)
	out.last().(playback.Failer).Fail(errors.New("decoder gave up"))
	waitFor(t, p, Failed)
}

func TestAStreamThatRunsOutEndsQuietly(t *testing.T) {
	p, out := playing(t)
	out.last().(playback.Failer).Fail(io.ErrUnexpectedEOF)
	waitFor(t, p, Idle)
}

func TestANewVideoReplacesTheOldOne(t *testing.T) {
	p, out := playing(t)
	first := out.last()
	if err := p.Play(context.Background(), serve(t)+"/b.mp4", "Second", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, Playing)
	if out.last() == first {
		t.Fatal("the new video did not replace the old one")
	}
	if got := out.last().Showing().Title; got != "Second" {
		t.Errorf("the title shown is %q, want Second", got)
	}
	first.(playback.Finisher).Finished()
	if p.State() != Playing {
		t.Errorf("the end of the replaced video left the state %s", p.State())
	}
}

func TestStoppingTheRunStopsTheVideo(t *testing.T) {
	p, _ := playing(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, Idle)
}

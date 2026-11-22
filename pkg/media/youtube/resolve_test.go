package youtube

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/media/webm"
)

func TestAVideoStreamIsVP9(t *testing.T) {
	if os.Getenv("COUNTERTOP_LIVE") == "" {
		t.Skip("set COUNTERTOP_LIVE=1 to ask YouTube")
	}
	r := NewResolver(&http.Client{Timeout: time.Minute}, nil)
	_, stream, err := r.Video(t.Context(), "Y7nDX15KJ5M")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	head := make([]byte, 4<<20)
	n, err := io.ReadFull(stream, head)
	if err != nil && n == 0 {
		t.Fatal(err)
	}
	head = head[:n]
	if cut := bytes.LastIndex(head, []byte{0x1F, 0x43, 0xB6, 0x75}); cut > 0 {
		head = head[:cut]
	}
	tr, err := webm.NewVideoReader(bytes.NewReader(head)).Track()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %dx%d, %d bytes kept", tr.Codec, tr.Width, tr.Height, len(head))
	if tr.Codec != "V_VP9" || tr.Height == 0 {
		t.Errorf("track %+v", tr)
	}
}

func TestTheTallestVP9ThatTheDecoderKeepsUpWithIsChosen(t *testing.T) {
	formats := []format{
		{ItagNo: 251, MimeType: `audio/webm; codecs="opus"`, Bitrate: 147426},
		{
			ItagNo:   137,
			MimeType: `video/mp4; codecs="avc1.640028"`,
			Height:   1080,
			FPS:      30,
			Bitrate:  4000000,
		},
		{ItagNo: 247, MimeType: `video/webm; codecs="vp9"`, Height: 720, FPS: 30, Bitrate: 1500000},
		{
			ItagNo:   248,
			MimeType: `video/webm; codecs="vp9"`,
			Height:   1080,
			FPS:      30,
			Bitrate:  2500000,
		},
		{
			ItagNo:   303,
			MimeType: `video/webm; codecs="vp9"`,
			Height:   1080,
			FPS:      60,
			Bitrate:  4000000,
		},
		{
			ItagNo:   271,
			MimeType: `video/webm; codecs="vp9"`,
			Height:   1440,
			FPS:      30,
			Bitrate:  9000000,
		},
	}
	limits := playback.Target{Tallest: 1080, Fastest: 60}
	if f := bestVP9(formats, limits); f == nil || f.ItagNo != 303 {
		t.Errorf("chose %+v", f)
	}
	if f := bestVP9([]format{formats[4]}, limits); f == nil || f.ItagNo != 303 {
		t.Errorf("with only 60 fps on offer, chose %+v", f)
	}
	if f := bestVP9(formats[:2], limits); f != nil {
		t.Errorf("chose %d from a list with no vp9", f.ItagNo)
	}
}

func TestTheTallestPictureFollowsThePanel(t *testing.T) {
	formats := []format{
		{
			ItagNo:   248,
			MimeType: `video/webm; codecs="vp9"`,
			Height:   1080,
			FPS:      30,
			Bitrate:  2500000,
		},
		{ItagNo: 247, MimeType: `video/webm; codecs="vp9"`, Height: 720, FPS: 30, Bitrate: 1200000},
		{ItagNo: 244, MimeType: `video/webm; codecs="vp9"`, Height: 480, FPS: 30, Bitrate: 600000},
	}
	if f := bestVP9(formats, playback.Target{Tallest: 720}); f == nil || f.ItagNo != 247 {
		t.Fatalf("a 720 line cap chose %v", f)
	}
	r := NewResolver(nil, func() playback.Target { return playback.Target{Tallest: 480} })
	if f := bestVP9(formats, r.limits()); f == nil || f.ItagNo != 244 {
		t.Fatalf("the resolver's target chose %v", f)
	}
}

func TestTenBitVP9IsPassedOver(t *testing.T) {
	formats := []format{
		{
			ItagNo:   332,
			MimeType: `video/webm; codecs="vp9.2"`,
			Height:   1080,
			FPS:      30,
			Bitrate:  3000000,
		},
		{ItagNo: 247, MimeType: `video/webm; codecs="vp9"`, Height: 720, FPS: 30, Bitrate: 1200000},
	}
	if f := bestVP9(formats, playback.Target{}); f == nil || f.ItagNo != 247 {
		t.Fatalf("chose %v over plain vp9", f)
	}
	if f := bestVP9(formats[:1], playback.Target{}); f != nil {
		t.Errorf("a list of only 10-bit vp9 chose %v", f)
	}
}

func TestATrackWithoutAPanelHasNoPictures(t *testing.T) {
	if (&Track{r: NewResolver(nil, nil)}).Pictured() {
		t.Error("a resolver with no video target offered pictures")
	}
	pictured := &Track{r: NewResolver(nil, func() playback.Target { return playback.Target{} })}
	if !pictured.Pictured() {
		t.Error("a resolver with a video target offered no pictures")
	}
}

func TestOpusIsChosenOverAACAndAACLCWhenThereIsNoOpus(t *testing.T) {
	formats := []format{
		{ItagNo: 139, MimeType: `audio/mp4; codecs="mp4a.40.5"`, Bitrate: 49000},
		{ItagNo: 140, MimeType: `audio/mp4; codecs="mp4a.40.2"`, Bitrate: 131104},
		{ItagNo: 249, MimeType: `audio/webm; codecs="opus"`, Bitrate: 55420},
		{ItagNo: 251, MimeType: `audio/webm; codecs="opus"`, Bitrate: 147426},
		{ItagNo: 248, MimeType: `video/webm; codecs="vp9"`, Bitrate: 2000000},
	}
	if f := bestAudio(formats); f == nil || f.ItagNo != 251 {
		t.Errorf("chose %+v", f)
	}
	if f := bestAudio(formats[:2]); f == nil || f.ItagNo != 140 {
		t.Errorf("chose %+v from a list with no opus", f)
	}
	if f := bestAudio(formats[:1]); f != nil {
		t.Errorf("chose %d from a list with only HE-AAC", f.ItagNo)
	}
}

func TestTheOriginalLanguageIsChosenOverADubOrAProcessedCopy(t *testing.T) {
	const opus = `audio/webm; codecs="opus"`
	at := func(xtags string) string {
		return "https://gv.example/videoplayback?itag=251&xtags=" + url.QueryEscape(xtags)
	}
	def := format{AudioTrack: &audioTrack{AudioIsDefault: true}}
	dub := format{AudioTrack: &audioTrack{}}
	with := func(track format, bitrate int, xtags string) format {
		track.ItagNo, track.MimeType, track.Bitrate, track.URL = 251, opus, bitrate, at(xtags)
		return track
	}

	for _, c := range []struct {
		name    string
		formats []format
		want    int
	}{
		{"a louder dub, and processed copies of the original", []format{
			with(dub, 158163, "acont=dubbed:lang=es-US"),
			with(def, 150000, "acont=original:lang=en-US:drc=1"),
			with(def, 140000, "acont=original:lang=en-US:vb=1"),
			with(def, 128000, "acont=original:lang=en-US"),
		}, 128000},
		{"only the default flag to go on", []format{
			with(dub, 158163, ""),
			with(def, 120000, ""),
		}, 120000},
		{"one language, one of them compressed", []format{
			with(format{}, 124446, "drc=1"),
			with(format{}, 112788, ""),
		}, 112788},
	} {
		if f := bestAudio(c.formats); f == nil || f.Bitrate != c.want {
			t.Errorf("%s: chose %+v, want the one at %d", c.name, f, c.want)
		}
	}
}

func TestAChunkIsFifteenSecondsOfItsStream(t *testing.T) {
	for _, at := range []struct {
		f    format
		want int64
	}{
		{format{ItagNo: 251, ContentLength: 3012048, ApproxDurationMs: "173121"}, 260970},
		{format{ItagNo: 248, ContentLength: 6934788, ApproxDurationMs: "173080"}, 600990},
		{format{ItagNo: 1, ContentLength: 3012048}, chunkFloor},
		{format{ItagNo: 2, ContentLength: 1000, ApproxDurationMs: "173121"}, chunkFloor},
	} {
		if got := chunkFor(&at.f); got != at.want {
			t.Errorf("itag %d: chunk %d, want %d", at.f.ItagNo, got, at.want)
		}
	}
}

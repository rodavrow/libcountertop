package hls

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	tscodecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
)

func videoSegment(t *testing.T, key bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	track := &mpegts.Track{Codec: &tscodecs.H264{}}
	w := &mpegts.Writer{W: &buf, Tracks: []*mpegts.Track{track}}
	if err := w.Initialize(); err != nil {
		t.Fatal(err)
	}
	au := [][]byte{{0x41, 0x9a}}
	if key {
		au = [][]byte{{0x67, 0x4d, 0x40, 0x1f}, {0x68, 0xee, 0x3c, 0x80}, {0x65, 0x88}}
	}
	for i := range 3 {
		if err := w.WriteH264(track, int64(i)*3000, int64(i)*3000, au); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func liveVideo(t *testing.T, keys []bool, dated bool) (string, time.Time) {
	t.Helper()
	wall := time.Now().
		Add(-time.Duration(len(keys)) * 5 * time.Second).
		UTC().
		Truncate(time.Millisecond)
	mux := http.NewServeMux()
	mux.HandleFunc("/video.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		var b strings.Builder
		b.WriteString(
			"#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:5\n#EXT-X-MEDIA-SEQUENCE:40\n",
		)
		if dated {
			fmt.Fprintf(
				&b,
				"#EXT-X-PROGRAM-DATE-TIME:%s\n",
				wall.Format("2006-01-02T15:04:05.000Z"),
			)
		}
		for i := range keys {
			fmt.Fprintf(&b, "#EXTINF:5.0,\n/seg%d.ts\n", i)
		}
		_, _ = w.Write([]byte(b.String()))
	})
	for i, key := range keys {
		seg := videoSegment(t, key)
		mux.HandleFunc(fmt.Sprintf("/seg%d.ts", i), func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(seg)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/video.m3u8", wall
}

func TestALiveStartGoesBackToTheLastKeyFrame(t *testing.T) {
	u, wall := liveVideo(t, []bool{false, true, false, false, false, false, false, false}, true)
	s := &Stream{o: Options{Lead: liveLead}}
	got := s.keyedStart(t.Context(), u)
	if want := wall.Add(5 * time.Second); !got.Equal(want) {
		t.Errorf("started at %v, want the key frame segment at %v", got, want)
	}
}

func TestALiveStartWithoutAKeyFrameOrClockKeepsTheUsualStart(t *testing.T) {
	u, _ := liveVideo(t, []bool{false, false, false, false, false, false}, true)
	s := &Stream{o: Options{Lead: liveLead}}
	if got := s.keyedStart(t.Context(), u); !got.IsZero() {
		t.Errorf("no key frame in the window gave a start at %v", got)
	}
	u, _ = liveVideo(t, []bool{true, false, false, false, false, false}, false)
	if got := s.keyedStart(t.Context(), u); !got.IsZero() {
		t.Errorf("a playlist without a clock gave a start at %v", got)
	}
}

func TestOnlyAnIDRWithItsSPSCountsAsAKeyFrame(t *testing.T) {
	if !keyed(videoSegment(t, true)) {
		t.Error("a segment opening on an IDR with its SPS was not keyed")
	}
	if keyed(videoSegment(t, false)) {
		t.Error("a segment of plain slices was keyed")
	}
}

package hls

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSoundOnlyNeverFetchesThePicture(t *testing.T) {
	audio := &fakeLive{t: t, segments: 3, ended: true}
	var pictures atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("#EXTM3U\n" +
			`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a",NAME="Default",DEFAULT=YES,URI="/audio.m3u8"` + "\n" +
			`#EXT-X-STREAM-INF:BANDWIDTH=2000000,CODECS="avc1.4D4020,mp4a.40.2",RESOLUTION=1280x720,AUDIO="a"` + "\n" +
			"/video.m3u8\n"))
	})
	mux.HandleFunc("/video.m3u8", func(w http.ResponseWriter, r *http.Request) {
		pictures.Add(1)
		http.NotFound(w, r)
	})
	mux.Handle("/", audio)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Open(ctx, srv.URL+"/master.m3u8", Options{SoundOnly: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	buf := make([]int16, 4096)
	heard := 0
	for heard == 0 && ctx.Err() == nil {
		n, err := s.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		heard += n
		if n == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if heard == 0 {
		t.Fatal("no sound arrived")
	}
	if n := pictures.Load(); n != 0 {
		t.Errorf("fetched the picture playlist %d times", n)
	}
	if s.Width != 0 {
		t.Errorf("width %d, want no picture", s.Width)
	}
}

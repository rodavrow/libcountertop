package youtube

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type fakePlayer struct {
	mu      sync.Mutex
	seen    []string
	headers []http.Header
}

func (f *fakePlayer) serve(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /youtubei/v1/player", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Context struct {
				Client struct {
					Visitor string `json:"visitorData"`
				} `json:"client"`
			} `json:"context"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.seen = append(f.seen, body.Context.Client.Visitor)
		f.mu.Unlock()
		if body.Context.Client.Visitor == "" {
			_, _ = w.Write([]byte(`{"responseContext":{"visitorData":"Vk%3D%3D"},
				"playabilityStatus":{"status":"LOGIN_REQUIRED","reason":"bot"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"playabilityStatus":{"status":"OK"},
			"videoDetails":{"title":"A video","author":"Chan","lengthSeconds":"60"},
			"streamingData":{"adaptiveFormats":[{"itag":251,"mimeType":"audio/webm; codecs=\"opus\"",
				"bitrate":128000,"contentLength":"960000","approxDurationMs":"60000",
				"url":"` + "http://" + r.Host + `/media"}]}}`))
	})
	mux.HandleFunc("GET /media", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
	})
	mux.HandleFunc("POST /youtubei/v1/browse", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"responseContext":{"visitorData":"QQ%3D%3D"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := base
	base = srv.URL
	t.Cleanup(func() { base = old })
}

func TestAResolverWithoutAVisitorLearnsOneAndAsksAgain(t *testing.T) {
	f := &fakePlayer{}
	f.serve(t)
	r := NewResolver(http.DefaultClient, nil)
	info, err := r.Info(t.Context(), "v")
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "A video" || info.Duration != time.Minute {
		t.Errorf("info %+v", info)
	}
	if len(f.seen) != 2 || f.seen[0] != "" || f.seen[1] != "Vk==" {
		t.Errorf("visitor ids sent %q, want none and then the learned one", f.seen)
	}
}

func TestTheAnonymousSessionSendsItsVisitorAndNoSignIn(t *testing.T) {
	f := &fakePlayer{}
	f.serve(t)
	var kept []string
	v := NewVisitor("", func(id string) { kept = append(kept, id) })
	s := Anonymous(http.DefaultClient, v)
	if _, err := s.post(t.Context(), "browse", map[string]any{"browseId": Home}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.post(t.Context(), "browse", map[string]any{"browseId": Home}); err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0] != "QQ==" {
		t.Errorf("kept %q, want the first visitor id once", kept)
	}
	for i, h := range f.headers {
		if h.Get("Authorization") != "" {
			t.Errorf("request %d carried a sign-in", i)
		}
	}
	if got := f.headers[1].Get("X-Goog-Visitor-Id"); got != "QQ==" {
		t.Errorf("second request's visitor id %q, want QQ==", got)
	}
}

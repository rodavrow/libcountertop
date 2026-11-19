package youtube

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

type fakeTracking struct {
	mu      sync.Mutex
	players []player
	hits    []hit
}

type player struct {
	auth string
	body struct {
		Context struct {
			Client struct {
				Version string `json:"clientVersion"`
			} `json:"client"`
		} `json:"context"`
		Playback struct {
			Content struct {
				Timestamp int `json:"signatureTimestamp"`
			} `json:"contentPlaybackContext"`
		} `json:"playbackContext"`
	}
}

type hit struct {
	path string
	auth string
	q    url.Values
}

func (f *fakeTracking) serve(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /youtubei/v1/player", func(w http.ResponseWriter, r *http.Request) {
		p := player{auth: r.Header.Get("Authorization")}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &p.body)
		f.mu.Lock()
		f.players = append(f.players, p)
		f.mu.Unlock()
		who := "anon"
		if p.auth != "" {
			who = "signed"
		}
		_, _ = w.Write([]byte(`{"playabilityStatus":{"status":"OK"},
			"videoDetails":{"title":"A video","author":"Chan","lengthSeconds":"60"},
			"streamingData":{"adaptiveFormats":[{"itag":251,"mimeType":"audio/webm; codecs=\"opus\"",
				"bitrate":128000,"contentLength":"960000","approxDurationMs":"60000",
				"url":"http://` + r.Host + `/media"}]},
			"playbackTracking":{
				"videostatsPlaybackUrl":{"baseUrl":"http://` + r.Host + `/playback?who=` + who + `"},
				"videostatsWatchtimeUrl":{"baseUrl":"http://` + r.Host + `/watchtime?who=` + who + `"}}}`))
	})
	record := func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits = append(f.hits, hit{r.URL.Path, r.Header.Get("Authorization"), r.URL.Query()})
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
	mux.HandleFunc("GET /playback", record)
	mux.HandleFunc("GET /watchtime", record)
	mux.HandleFunc("GET /media", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := base
	base = srv.URL
	t.Cleanup(func() { base = old })
}

func (f *fakeTracking) seen(path string, within time.Duration) []hit {
	deadline := time.Now().Add(within)
	for {
		f.mu.Lock()
		var got []hit
		for _, h := range f.hits {
			if h.path == path {
				got = append(got, h)
			}
		}
		f.mu.Unlock()
		if len(got) > 0 || time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func signedIn() *Resolver {
	r := NewResolver(http.DefaultClient, nil)
	s := SignedIn(
		http.DefaultClient,
		Client{},
		Token{Access: "tok", Expiry: time.Now().Add(time.Hour)},
	)
	r.Account = func() *Session { return s }
	return r
}

func reportShape(t *testing.T, h hit) {
	t.Helper()
	want := map[string]string{
		"c":      "TVHTML5",
		"cver":   tvReportVersion,
		"cbrver": tvReportVersion,
		"ver":    "2",
		"cmt":    "0",
		"final":  "1",
		"who":    "signed",
	}
	for k, v := range want {
		if got := h.q.Get(k); got != v {
			t.Errorf("%s %s=%q, want %q", h.path, k, got, v)
		}
	}
	if h.auth != "Bearer tok" {
		t.Errorf("%s sent as %q, want the account", h.path, h.auth)
	}
}

func TestTheAccountsReplyCarriesTheStartPingAndWatchReports(t *testing.T) {
	f := &fakeTracking{}
	f.serve(t)
	_, stream, reports, err := signedIn().audio(t.Context(), "v", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()

	pings := f.seen("/playback", 2*time.Second)
	if len(pings) != 1 {
		t.Fatalf("start pings %+v, want one", pings)
	}
	reportShape(t, pings[0])
	reports.report(35 * time.Second)
	got := f.seen("/watchtime", 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("watch reports %+v, want one", got)
	}
	reportShape(t, got[0])
	if got[0].q.Get("st") != "35.000" || got[0].q.Get("et") != "35.000" {
		t.Errorf(
			"watch report st=%s et=%s, want 35 for both",
			got[0].q.Get("st"),
			got[0].q.Get("et"),
		)
	}
	if c := got[0].q.Get("cpn"); c == "" || c != pings[0].q.Get("cpn") {
		t.Errorf("watch report cpn %q, start ping cpn %q, want the same", c, pings[0].q.Get("cpn"))
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.players) != 2 || f.players[0].auth != "" {
		t.Fatalf(
			"player requests %+v, want an anonymous one for playback, then the account's",
			f.players,
		)
	}
	signed := f.players[1]
	if signed.auth != "Bearer tok" || signed.body.Context.Client.Version != tvReportVersion ||
		signed.body.Playback.Content.Timestamp != signatureTimestamp {
		t.Errorf(
			"account's player request as %q, version %q, signature timestamp %d",
			signed.auth,
			signed.body.Context.Client.Version,
			signed.body.Playback.Content.Timestamp,
		)
	}
}

func TestASeekKeepsThePlaySession(t *testing.T) {
	f := &fakeTracking{}
	f.serve(t)
	r := signedIn()
	_, stream, first, err := r.audio(t.Context(), "v", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	f.seen("/playback", 2*time.Second)
	_, stream, again, err := r.audio(t.Context(), "v", first)
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	if again != first {
		t.Fatal("a seek started a new play session")
	}
	again.report(100 * time.Second)
	got := f.seen("/watchtime", 2*time.Second)
	if len(got) != 1 || got[0].q.Get("st") != "100.000" || got[0].q.Get("et") != "100.000" {
		t.Fatalf("watch reports %+v, want one at 100", got)
	}
	if pings := f.seen("/playback", 0); len(pings) != 1 {
		t.Errorf("start pings %+v, want one for the whole play session", pings)
	}
}

func TestAnAnonymousPlayReportsNothing(t *testing.T) {
	f := &fakeTracking{}
	f.serve(t)
	r := NewResolver(http.DefaultClient, nil)
	profile := NewVisitor("PANEL", nil)
	r.Profile = func() *Visitor { return profile }
	_, stream, reports, err := r.audio(t.Context(), "v", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream.Close()
	if reports != nil {
		t.Error("an anonymous play started a play session")
	}
	if pings := f.seen("/playback", 200*time.Millisecond); len(pings) != 0 {
		t.Errorf("an anonymous play sent start pings: %+v", pings)
	}
}

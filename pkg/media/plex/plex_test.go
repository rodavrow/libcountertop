package plex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ygelfand/plexgo/models/sdkerrors"
)

type fake struct {
	mu             sync.Mutex
	polls          int
	reported       url.Values
	decided        url.Values
	decidedHeaders http.Header
	part           string
	started        url.Values
	photo          url.Values
	paths          []string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.Host+r.URL.Path)
	f.mu.Unlock()
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.URL.Path == "/api/v2/pins" && r.Method == http.MethodPost:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 77, "code": "WXYZ", "expiresIn": 900})
	case r.URL.Path == "/api/v2/pins/77":
		f.mu.Lock()
		f.polls++
		n := f.polls
		f.mu.Unlock()
		if n < 2 {
			reply(map[string]any{"id": 77, "code": "WXYZ"})
			return
		}
		reply(map[string]any{"id": 77, "code": "WXYZ", "authToken": "account-token"})
	case r.URL.Path == "/api/v2/resources":
		reply([]map[string]any{
			{
				"name":             "Phone",
				"provides":         "client,player",
				"clientIdentifier": "p1",
				"accessToken":      "x",
			},
			{
				"name": "Den", "provides": "server", "clientIdentifier": "m1",
				"accessToken": "server-token", "owned": true,
				"connections": []map[string]any{
					{"uri": "http://far.example:32400", "local": false, "relay": false},
					{"uri": "http://near.example:32400", "local": true, "relay": false},
					{"uri": "http://dead.example:32400", "local": true, "relay": false},
				},
			},
		})
	case r.URL.Path == "/api/v2/home/users":
		reply(map[string]any{"users": []map[string]any{
			{"uuid": "u-admin", "title": "Owner", "admin": true},
			{"uuid": "u-kid", "title": "Kid", "protected": true},
		}})
	case r.URL.Path == "/api/v2/home/users/u-kid/switch":
		var body struct {
			Pin string `json:"pin"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Pin != "1234" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusCreated)
		reply(map[string]any{"authToken": "kid-token"})
	case r.URL.Path == "/identity":
		if strings.HasPrefix(r.Host, "dead.") {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		reply(map[string]any{"MediaContainer": map[string]any{"machineIdentifier": "m1"}})
	case r.URL.Path == "/hubs/promoted":
		reply(map[string]any{"MediaContainer": map[string]any{"Hub": []map[string]any{{
			"title": "Continue Watching", "key": "/hubs/cw",
			"Metadata": []map[string]any{{
				"ratingKey": "42", "key": "/library/metadata/42", "type": "episode",
				"title": "Pilot", "grandparentTitle": "A Show", "parentIndex": 1, "index": 1,
				"duration": 1800000, "viewOffset": 600000, "thumb": "/t/42",
				"Media": []map[string]any{{
					"id": 1, "container": "mkv", "videoCodec": "h264", "audioCodec": "aac",
					"Part": []map[string]any{{"id": 1, "key": "/library/parts/1/file.mkv"}},
				}},
			}},
		}}}})
	case r.URL.Path == "/video/:/transcode/universal/decision":
		f.mu.Lock()
		f.decided = r.URL.Query()
		f.decidedHeaders = r.Header.Clone()
		f.mu.Unlock()
		f.mu.Lock()
		part := f.part
		f.mu.Unlock()
		reply(map[string]any{"MediaContainer": map[string]any{
			"generalDecisionCode": 1001,
			"generalDecisionText": "Direct play not available; Conversion OK.",
			"Metadata": []map[string]any{{"key": "/library/metadata/42", "Media": []map[string]any{
				{
					"id":        "674965",
					"container": "mpegts",
					"Part": []map[string]any{
						{"id": "679806", "key": "/p", "decision": part, "container": "mkv"},
					},
				},
			}}},
		}})
	case r.URL.Path == "/video/:/transcode/universal/start.m3u8":
		f.mu.Lock()
		f.started = r.URL.Query()
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-STREAM-INF:PROGRAM-ID=1,BANDWIDTH=5570000," +
			"RESOLUTION=1920x1012,FRAME-RATE=24.000000\nsession/s1/base/index.m3u8\n"))
	case r.URL.Path == "/photo/:/transcode":
		f.mu.Lock()
		f.photo = r.URL.Query()
		f.mu.Unlock()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg"))
	case r.URL.Path == "/:/timeline":
		f.mu.Lock()
		f.reported = r.URL.Query()
		f.mu.Unlock()
		reply(map[string]any{})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type everyHost struct{ to *url.URL }

func (e everyHost) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = r.URL.Host
	r.URL.Scheme, r.URL.Host = e.to.Scheme, e.to.Host
	return http.DefaultTransport.RoundTrip(r)
}

func rig(t *testing.T) (Account, *fake) {
	t.Helper()
	f := &fake{}
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	to, _ := url.Parse(ts.URL)
	hc := &http.Client{Transport: everyHost{to: to}, Timeout: 5 * time.Second}
	return Account{ID: Identity{Client: "device-1", Product: "Test"}, HTTP: hc}, f
}

func TestAnErrorKeepsTheStatusAndDropsTheReply(t *testing.T) {
	err := quiet("asking", sdkerrors.NewSDKError("unknown status code returned", 201,
		`{"location":{"city":"Somewhere"}}`, nil))
	if strings.Contains(err.Error(), "Somewhere") || !strings.Contains(err.Error(), "HTTP 201") {
		t.Errorf("got %q, want the status without the reply", err)
	}
	if !errors.Is(quiet("asking", sdkerrors.NewSDKError("no", 401, "x", nil)), ErrToken) {
		t.Error("a 401 should be ErrToken")
	}
}

func TestSigningInGivesTheCodeThenTheToken(t *testing.T) {
	a, _ := rig(t)
	ctx := context.Background()
	pin, err := a.Ask(ctx)
	if err != nil || pin.Code != "WXYZ" || pin.URL != "plex.tv/link" ||
		pin.QR != "https://www.plex.tv/link/?token=WXYZ" {
		t.Fatalf("pin %+v, %v", pin, err)
	}
	token, err := a.Await(ctx, pin)
	if err != nil || token != "account-token" {
		t.Fatalf("token %q, %v", token, err)
	}
}

func TestTheServerIsReachedOnItsNearestAnsweringConnection(t *testing.T) {
	a, _ := rig(t)
	ctx := context.Background()
	servers, err := a.Servers(ctx, "account-token")
	if err != nil || len(servers) != 1 || servers[0].Machine != "m1" {
		t.Fatalf("servers %+v, %v", servers, err)
	}
	lib, err := a.Connect(ctx, servers[0])
	if err != nil {
		t.Fatal(err)
	}
	if lib.Base != "http://near.example:32400" || lib.Token != "server-token" || !lib.Local {
		t.Errorf(
			"connected to %s with %q, want the local answering connection",
			lib.Base,
			lib.Token,
		)
	}
}

func TestAProtectedProfileNeedsItsPIN(t *testing.T) {
	a, _ := rig(t)
	ctx := context.Background()
	profiles, err := a.Profiles(ctx, "account-token")
	if err != nil || len(profiles) != 2 || !profiles[1].Protected {
		t.Fatalf("profiles %+v, %v", profiles, err)
	}
	if _, err := a.Switch(ctx, "account-token", "u-kid", "0000"); !errors.Is(err, ErrPIN) {
		t.Errorf("wrong PIN gave %v, want ErrPIN", err)
	}
	token, err := a.Switch(ctx, "account-token", "u-kid", "1234")
	if err != nil || token != "kid-token" {
		t.Errorf("switch gave %q, %v", token, err)
	}
}

func TestAPictureIsAskedOfThisServerWithoutTheTokenInItsKey(t *testing.T) {
	a, f := rig(t)
	ctx := context.Background()
	lib := &Library{ID: a.ID, HTTP: a.HTTP, Base: "http://near.example:32400",
		Token: "server-token", Machine: "m1"}
	key := lib.Picture("/library/metadata/42/thumb/1", 200, 300)
	if strings.Contains(key, "server-token") {
		t.Fatalf("key %q carries the token", key)
	}
	got, err := lib.fetch(ctx, key)
	if err != nil || string(got) != "jpeg" {
		t.Fatalf("picture %q, %v", got, err)
	}
	f.mu.Lock()
	asked := f.photo
	f.mu.Unlock()
	if asked.Get("url") != "/library/metadata/42/thumb/1" || asked.Get("width") != "200" ||
		asked.Get("height") != "300" {
		t.Errorf("asked %v", asked)
	}
	other := &Library{ID: a.ID, HTTP: a.HTTP, Base: lib.Base, Token: lib.Token, Machine: "m2"}
	if _, err := other.fetch(ctx, key); !errors.Is(err, ErrNoServer) {
		t.Errorf("another server's picture gave %v", err)
	}
}

func TestHomeRowPlaysDirectAndReportsProgress(t *testing.T) {
	a, f := rig(t)
	ctx := context.Background()
	lib := &Library{
		ID:    a.ID,
		HTTP:  a.HTTP,
		Base:  "http://near.example:32400",
		Token: "server-token",
	}
	hubs, err := lib.Promoted(ctx)
	if err != nil || len(hubs) != 1 || len(hubs[0].Items) != 1 {
		t.Fatalf("hubs %+v, %v", hubs, err)
	}
	ep := hubs[0].Items[0]
	if ep.Show != "A Show" || ep.Offset != 10*time.Minute || ep.Progress() < 0.33 ||
		ep.Progress() > 0.34 {
		t.Errorf("episode %+v", ep)
	}
	athena := Decoder{Width: 1920, Height: 1088}
	f.mu.Lock()
	f.part = "directplay"
	f.mu.Unlock()
	s, err := lib.Stream(ctx, ep, athena, "s1")
	if err != nil || !s.Direct ||
		!strings.HasSuffix(strings.Split(s.URL, "?")[0], "/library/parts/1/file.mkv") ||
		s.ContentType != "video/x-matroska" {
		t.Errorf("a direct-play decision should open the file, got %+v, %v", s, err)
	}
	f.mu.Lock()
	decided, headers := f.decided, f.decidedHeaders
	f.mu.Unlock()
	extra := headers.Get("X-Plex-Client-Profile-Extra")
	for _, want := range []string{
		"add-direct-play-profile(type=videoProfile&container=mkv&videoCodec=h264,hevc&audioCodec=aac,opus,mp3,flac,vorbis&subtitleCodec=*)",
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.height&value=1088&isRequired=true)",
		"add-limitation(scope=videoCodec&scopeName=hevc&type=upperBound&name=video.width&value=1920&isRequired=true)",
		"add-limitation(scope=videoCodec&scopeName=h264&type=upperBound&name=video.bitDepth&value=8&isRequired=true)",
	} {
		if !strings.Contains(extra, want) {
			t.Errorf("profile lacks %s\nprofile: %s", want, extra)
		}
	}
	if decided.Get("directPlay") != "1" || decided.Get("directStream") != "1" ||
		decided.Get("fastSeek") != "1" || decided.Get("copyts") != "1" ||
		decided.Get("directStreamAudio") != "1" || decided.Has("videoResolution") ||
		decided.Get(
			"path",
		) != "/library/metadata/42" || headers.Get("X-Plex-Session-Identifier") != "s1" {
		t.Errorf("decision asked %v with %v", decided, headers)
	}
	f.mu.Lock()
	f.part = "transcode"
	f.mu.Unlock()
	s, err = lib.Stream(ctx, ep, athena, "s1")
	if err != nil || s.Direct || s.ContentType != HLSType ||
		s.URL != "http://near.example:32400/video/:/transcode/universal/session/s1/base/index.m3u8" {
		t.Fatalf("a transcode decision should open the media playlist, got %+v, %v", s, err)
	}
	f.mu.Lock()
	started := f.started
	f.mu.Unlock()
	if started.Get("transcodeSessionId") != "s1" || started.Get("directStream") != "1" ||
		started.Get("fastSeek") != "1" || started.Get("copyts") != "1" {
		t.Errorf("start asked %v", started)
	}
	if err := lib.Report(ctx, ep, Playing, 11*time.Minute, ep.Duration, "s1"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	got := f.reported
	f.mu.Unlock()
	if got.Get("key") != "/library/metadata/42" || got.Get("state") != "playing" ||
		got.Get("time") != "660000" {
		t.Errorf("timeline %v", got)
	}
}

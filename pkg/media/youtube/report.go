package youtube

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

const (
	tvReportVersion    = "7.20260611.00.00"
	tvNumber           = "7"
	tvAgent            = "Mozilla/5.0 (DirectFB; Linux x86_64) Cobalt/4.13031-qa (unlike Gecko) Starboard/1"
	signatureTimestamp = 1000000
	reportWithin       = 20 * time.Second
)

type watch struct {
	http *http.Client
	cpn  string

	mu      sync.Mutex
	address string
	track   func(*http.Request)
}

func (r *Resolver) account() *Session {
	if r.Account == nil {
		return nil
	}
	return r.Account()
}

func (r *Resolver) report(id string) *watch {
	s := r.account()
	if s == nil {
		return nil
	}
	w := &watch{http: r.http, cpn: nonce()}
	safe.Go("youtube signed-in tracking", func() { w.sign(id, s) })
	return w
}

func (w *watch) sign(id string, s *Session) {
	ctx, cancel := context.WithTimeout(context.Background(), reportWithin)
	defer cancel()
	got, bearer, err := w.signed(ctx, id, s)
	if err != nil {
		slog.Warn("youtube signed-in tracking", "video", id, "err", err)
		return
	}
	w.send("youtube playback ping", got.Tracking.Playback.URL, bearer, nil)
	w.mu.Lock()
	w.address, w.track = got.Tracking.Watchtime.URL, bearer
	w.mu.Unlock()
}

func (w *watch) signed(
	ctx context.Context,
	id string,
	s *Session,
) (watched, func(*http.Request), error) {
	started := time.Now()
	access, err := s.access(ctx)
	if err != nil {
		return watched{}, nil, err
	}
	bearer := func(req *http.Request) {
		req.Header.Set("User-Agent", tvAgent)
		req.Header.Set("Authorization", "Bearer "+access)
	}
	r := Resolver{http: w.http}
	got, _, err := r.player(ctx, map[string]any{
		"videoId":        id,
		"contentCheckOk": true,
		"racyCheckOk":    true,
		"context": map[string]any{"client": map[string]any{
			"clientName":    tvClientName,
			"clientVersion": tvReportVersion,
			"userAgent":     tvAgent,
			"hl":            "en",
			"gl":            "US",
		}},
		"playbackContext": map[string]any{"contentPlaybackContext": map[string]any{
			"signatureTimestamp": signatureTimestamp,
		}},
	}, func(req *http.Request) {
		bearer(req)
		req.Header.Set("X-Youtube-Client-Name", tvNumber)
		req.Header.Set("X-Youtube-Client-Version", tvReportVersion)
	})
	err = playable(id, got, err)
	slog.Debug("youtube signed-in tracking", "video", id, "took", time.Since(started), "err", err)
	if err != nil {
		return watched{}, nil, err
	}
	return got, bearer, nil
}

func (w *watch) report(at time.Duration) {
	w.mu.Lock()
	address, track := w.address, w.track
	w.mu.Unlock()
	if address == "" {
		return
	}
	w.send("youtube watch report", address, track, map[string]string{
		"st": seconds(at),
		"et": seconds(at),
	})
}

func (w *watch) send(what, address string, track func(*http.Request), params map[string]string) {
	u, err := url.Parse(address)
	if address == "" || err != nil {
		return
	}
	if rest, ok := strings.CutPrefix(u.Host, "s."); ok {
		u.Host = "www." + rest
	}
	q := u.Query()
	for k, v := range map[string]string{
		"c":      tvClientName,
		"cver":   tvReportVersion,
		"cbrver": tvReportVersion,
		"ver":    "2",
		"cpn":    w.cpn,
		"cmt":    "0",
		"final":  "1",
	} {
		q.Set(k, v)
	}
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	safe.Go(what, func() {
		ctx, cancel := context.WithTimeout(context.Background(), reportWithin)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return
		}
		track(req)
		status, _, err := send(w.http, req)
		slog.Debug(what, "status", status, "at", q.Get("et"), "err", err)
	})
}

func seconds(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }

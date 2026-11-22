package youtube

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/fetch"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
)

const (
	visionName    = "VISIONOS"
	visionVersion = "1.02"
	visionAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15"
	visionModel   = "RealityDevice17,1"
	nonceLetters  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

type format struct {
	ItagNo           int         `json:"itag"`
	URL              string      `json:"url"`
	MimeType         string      `json:"mimeType"`
	Bitrate          int         `json:"bitrate"`
	Width            int         `json:"width"`
	Height           int         `json:"height"`
	FPS              int         `json:"fps"`
	ContentLength    int64       `json:"contentLength,string"`
	ApproxDurationMs string      `json:"approxDurationMs"`
	AudioTrack       *audioTrack `json:"audioTrack"`
	InitRange        *byteRange  `json:"initRange"`
	IndexRange       *byteRange  `json:"indexRange"`
}

type audioTrack struct {
	ID             string `json:"id"`
	AudioIsDefault bool   `json:"audioIsDefault"`
}

type byteRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type watched struct {
	Playability struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"playabilityStatus"`
	Details struct {
		Title     string `json:"title"`
		Author    string `json:"author"`
		Length    int64  `json:"lengthSeconds,string"`
		Thumbnail struct {
			Thumbnails []struct {
				URL string `json:"url"`
			} `json:"thumbnails"`
		} `json:"thumbnail"`
	} `json:"videoDetails"`
	Streaming struct {
		Formats []format `json:"adaptiveFormats"`
		HLS     string   `json:"hlsManifestUrl"`
	} `json:"streamingData"`
	Tracking struct {
		Playback struct {
			URL string `json:"baseUrl"`
		} `json:"videostatsPlaybackUrl"`
		Watchtime struct {
			URL string `json:"baseUrl"`
		} `json:"videostatsWatchtimeUrl"`
	} `json:"playbackTracking"`
}

type Info struct {
	ID        string
	Title     string
	Author    string
	Duration  time.Duration
	Thumbnail string

	Codec string
}

const (
	chunkTime  = 15 * time.Second
	chunkFloor = 64 << 10
	ahead      = 2
	videoAhead = 4
	lead       = 30 * time.Second
	retryAfter = 5 * time.Second
	plainVP9   = `video/webm; codecs="vp9"`
	codecOpus  = "opus"
	codecAAC   = "mp4a.40.2"
)

func rateOf(f *format) int64 {
	ms, err := strconv.ParseInt(f.ApproxDurationMs, 10, 64)
	if err != nil || ms <= 0 || f.ContentLength <= 0 {
		return 0
	}
	return f.ContentLength * 1000 / ms
}

func chunkFor(f *format) int64 {
	return max(rateOf(f)*int64(chunkTime/time.Second), chunkFloor)
}

type Resolver struct {
	http     *http.Client
	target   func() playback.Target
	visitor  *Visitor
	Resample func(from int) func(stereo []int16) []int16
	Profile  func() *Visitor
	Account  func() *Session
}

type Live struct {
	Info Info
	HLS  string
}

func (l *Live) Error() string { return fmt.Sprintf("youtube: %s is a live stream", l.Info.ID) }

func NewResolver(c *http.Client, target func() playback.Target) *Resolver {
	return &Resolver{http: c, target: target, visitor: NewVisitor("", nil)}
}

func (r *Resolver) Info(ctx context.Context, id string) (Info, error) {
	w, err := r.watch(ctx, id)
	if err != nil {
		return Info{}, err
	}
	return infoOf(id, w), nil
}

func live(w watched) bool { return w.Streaming.HLS != "" && w.Details.Length == 0 }

func (r *Resolver) Audio(ctx context.Context, id string) (Info, io.ReadCloser, error) {
	t, stream, _, err := r.audio(ctx, id, nil)
	return t, stream, err
}

func (r *Resolver) audio(
	ctx context.Context,
	id string,
	keep *watch,
) (Info, io.ReadCloser, *watch, error) {
	return r.open(ctx, id, "opus or aac audio", bestAudio, ahead, true, keep)
}

func (r *Resolver) Video(ctx context.Context, id string) (Info, io.ReadCloser, error) {
	t, stream, _, err := r.open(
		ctx,
		id,
		"vp9 video",
		func(f []format) *format { return bestVP9(f, r.limits()) },
		videoAhead,
		false,
		nil,
	)
	return t, stream, err
}

func (r *Resolver) viewer() *Visitor {
	if r.Profile != nil {
		if v := r.Profile(); v != nil {
			return v
		}
	}
	return r.visitor
}

func (r *Resolver) watch(ctx context.Context, id string) (watched, error) {
	started := time.Now()
	v := r.viewer()
	w, raw, err := r.vision(ctx, id, v.ID())
	if err == nil && w.Playability.Status != "OK" && v.ID() == "" {
		v.learn(raw)
		if v.ID() != "" {
			w, _, err = r.vision(ctx, id, v.ID())
		}
	}
	slog.Debug(
		"youtube player",
		"video", id,
		"took", time.Since(started),
		"status", w.Playability.Status,
		"err", err,
	)
	if err = playable(id, w, err); err != nil {
		return watched{}, err
	}
	return w, nil
}

func playable(id string, w watched, err error) error {
	if err != nil {
		return fmt.Errorf("youtube: resolving %s: %w", id, err)
	}
	if w.Playability.Status != "OK" {
		return fmt.Errorf(
			"youtube: resolving %s: %s: %s",
			id,
			w.Playability.Status,
			w.Playability.Reason,
		)
	}
	return nil
}

func (r *Resolver) vision(ctx context.Context, id, visitor string) (watched, []byte, error) {
	client := map[string]any{
		"clientName":    visionName,
		"clientVersion": visionVersion,
		"deviceModel":   visionModel,
		"userAgent":     visionAgent,
		"hl":            "en",
		"gl":            "US",
	}
	if visitor != "" {
		client["visitorData"] = visitor
	}
	return r.player(ctx, map[string]any{
		"videoId":        id,
		"contentCheckOk": true,
		"racyCheckOk":    true,
		"context":        map[string]any{"client": client},
	}, func(req *http.Request) {
		req.Header.Set("User-Agent", visionAgent)
		if visitor != "" {
			req.Header.Set("X-Goog-Visitor-Id", visitor)
		}
	})
}

func (r *Resolver) player(
	ctx context.Context,
	body map[string]any,
	dress func(*http.Request),
) (watched, []byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return watched{}, nil, err
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, base+"/youtubei/v1/player?prettyPrint=false", bytes.NewReader(raw),
	)
	if err != nil {
		return watched{}, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	dress(req)
	status, got, err := send(r.http, req)
	if err != nil {
		return watched{}, nil, err
	}
	if status/100 != 2 {
		return watched{}, got, fmt.Errorf("player: %d", status)
	}
	var w watched
	if err := json.Unmarshal(got, &w); err != nil {
		return watched{}, got, fmt.Errorf("player: %w", err)
	}
	return w, got, nil
}

func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = nonceLetters[int(b[i])%len(nonceLetters)]
	}
	return string(b)
}

func (r *Resolver) open(
	ctx context.Context,
	id, what string,
	pick func([]format) *format,
	ahead int,
	count bool,
	keep *watch,
) (Info, io.ReadCloser, *watch, error) {
	w, err := r.watch(ctx, id)
	if err != nil {
		return Info{}, nil, nil, err
	}
	if live(w) {
		return Info{}, nil, nil, &Live{Info: infoOf(id, w), HLS: w.Streaming.HLS}
	}

	formats := w.Streaming.Formats
	f := pick(formats)
	if f == nil {
		offered := make([]string, 0, len(formats))
		for _, c := range formats {
			offered = append(offered, fmt.Sprintf("%d:%s", c.ItagNo, c.MimeType))
		}
		slog.Warn(
			"youtube formats without a pick",
			"video",
			id,
			"want",
			what,
			"length",
			w.Details.Length,
			"hls",
			w.Streaming.HLS != "",
			"offered",
			strings.Join(offered, " "),
		)
		return Info{}, nil, nil, fmt.Errorf("youtube: %s has no %s", id, what)
	}
	if f.URL == "" {
		return Info{}, nil, nil, fmt.Errorf("youtube: %s format %d has no address", id, f.ItagNo)
	}
	for i := range formats {
		c := &formats[i]
		if c.ItagNo != f.ItagNo {
			continue
		}
		track := ""
		if c.AudioTrack != nil {
			track = c.AudioTrack.ID
		}
		slog.Debug(
			"youtube format",
			"video",
			id,
			"what",
			what,
			"chosen",
			c == f,
			"itag",
			c.ItagNo,
			"mime",
			c.MimeType,
			"bitrate",
			c.Bitrate,
			"length",
			c.ContentLength,
			"ms",
			c.ApproxDurationMs,
			"track",
			track,
			"init",
			c.InitRange,
			"index",
			c.IndexRange,
		)
	}
	slog.Debug(
		"youtube stream",
		"video",
		id,
		"what",
		what,
		"itag",
		f.ItagNo,
		"chunk",
		chunkFor(f),
		"rate",
		rateOf(f),
		"lead",
		lead,
		"ahead",
		ahead,
	)
	renew := func(ctx context.Context) (string, error) {
		fresh, err := r.watch(ctx, id)
		if err != nil {
			return "", err
		}
		if f := pick(fresh.Streaming.Formats); f != nil && f.URL != "" {
			return f.URL, nil
		}
		return "", fmt.Errorf("youtube: %s has no %s", id, what)
	}
	reports := keep
	if count && reports == nil {
		reports = r.report(id)
	}
	stream := fetch.NewRanged(
		ctx,
		r.http,
		f.URL,
		f.ContentLength,
		fetch.Chunks{
			Size:  chunkFor(f),
			Rate:  rateOf(f),
			Lead:  lead,
			Retry: retryAfter,
			Ahead: ahead,
			Ask:   ask,
			Renew: renew,
		},
	)

	info := infoOf(id, w)
	info.Codec = codecOf(f)
	return info, stream, reports, nil
}

func infoOf(id string, w watched) Info {
	d := w.Details
	t := Info{
		ID:       id,
		Title:    d.Title,
		Author:   d.Author,
		Duration: time.Duration(d.Length) * time.Second,
	}
	if thumbs := d.Thumbnail.Thumbnails; len(thumbs) > 0 {
		t.Thumbnail = thumbs[len(thumbs)-1].URL
	}
	return t
}

func ask(req *http.Request) {
	req.Header.Set("User-Agent", visionAgent)
	req.Header.Set("Origin", "https://youtube.com")
}

func (r *Resolver) limits() playback.Target {
	if r.target == nil {
		return playback.Target{}
	}
	return r.target()
}

func within(v, most int) bool { return most <= 0 || v <= most }

func bestVP9(formats []format, limits playback.Target) *format {
	var found []*format
	for i := range formats {
		f := &formats[i]
		if f.MimeType == plainVP9 && within(f.Height, limits.Tallest) {
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if within(a.FPS, limits.Fastest) != within(b.FPS, limits.Fastest) {
			return within(a.FPS, limits.Fastest)
		}
		if a.Height != b.Height {
			return a.Height > b.Height
		}
		return a.Bitrate > b.Bitrate
	})
	return found[0]
}

func bestAudio(formats []format) *format {
	if f := bestOf(formats, func(f *format) bool {
		return strings.HasPrefix(f.MimeType, "audio/webm") && codecOf(f) == codecOpus
	}); f != nil {
		return f
	}
	return bestOf(formats, func(f *format) bool {
		return strings.HasPrefix(f.MimeType, "audio/mp4") && codecOf(f) == codecAAC
	})
}

func codecOf(f *format) string {
	_, params, err := mime.ParseMediaType(f.MimeType)
	if err != nil {
		return ""
	}
	return params["codecs"]
}

func bestOf(formats []format, want func(*format) bool) *format {
	var found []*format
	for i := range formats {
		if f := &formats[i]; want(f) {
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.SliceStable(found, func(i, j int) bool {
		a, b := audioOf(found[i]), audioOf(found[j])
		if a.plain != b.plain {
			return a.plain
		}
		if a.original != b.original {
			return a.original
		}
		return found[i].Bitrate > found[j].Bitrate
	})
	return found[0]
}

type audio struct{ plain, original bool }

func audioOf(f *format) audio {
	tags := xtags(f.URL)
	a := audio{plain: tags["drc"] == "" && tags["vb"] == ""}
	switch {
	case tags["acont"] != "":
		a.original = tags["acont"] == "original"
	case f.AudioTrack != nil:
		a.original = f.AudioTrack.AudioIsDefault
	default:
		a.original = true
	}
	return a
}

func xtags(raw string) map[string]string {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	tags := map[string]string{}
	for _, pair := range strings.Split(u.Query().Get("xtags"), ":") {
		if k, v, ok := strings.Cut(pair, "="); ok {
			tags[k] = v
		}
	}
	return tags
}

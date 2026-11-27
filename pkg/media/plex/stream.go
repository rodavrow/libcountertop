package plex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/bluenviron/gohlslib/v2/pkg/playlist"
	"github.com/ygelfand/plexgo/models/components"
	"github.com/ygelfand/plexgo/models/operations"
)

const (
	HLSType      = "application/vnd.apple.mpegurl"
	startPath    = "/video/:/transcode/universal/start.m3u8"
	playlistMost = 1 << 20
)

var (
	directMusic = []string{"mp3", "aac", "flac", "opus", "ogg", "m4a"}
	trackAudio  = []string{"aac", "mp3", "opus", "flac", "vorbis"}
)

type Stream struct {
	URL         string
	ContentType string
	Direct      bool
	Session     string
}

func (l *Library) Stream(ctx context.Context, it Item, d Decoder, session string) (Stream, error) {
	if len(it.Media) == 0 || len(it.Media[0].Parts) == 0 {
		return Stream{}, fmt.Errorf("plex: %s has nothing to play", it.Title)
	}
	m, p := it.Media[0], it.Media[0].Parts[0]
	if it.Type == "track" {
		if !playsTrack(m) {
			return Stream{}, fmt.Errorf("plex: %s: %s audio does not play here", it.Title, m.Audio)
		}
		return Stream{
			URL:         l.file(p),
			ContentType: trackType(m),
			Direct:      true,
			Session:     session,
		}, nil
	}
	ask := asking{
		path: "/library/metadata/" + it.RatingKey, session: session, extra: d.profile(),
		local: l.Local,
	}
	res, err := l.api().Transcoder.MakeDecision(ctx, ask.decision())
	if err != nil {
		return Stream{}, quiet("transcode decision", err)
	}
	if err := status(res.StatusCode, "transcode decision"); err != nil {
		return Stream{}, err
	}
	logDecision(it, res.MediaContainerWithDecision)
	part, err := decided(res.MediaContainerWithDecision)
	if err != nil {
		return Stream{}, err
	}
	if part.Decision != nil && *part.Decision == components.DecisionDirectplay {
		kind, ok := containerTypes[strings.ToLower(deref(part.Container))]
		if !ok {
			return Stream{}, fmt.Errorf(
				"plex: direct play of a %s container",
				deref(part.Container),
			)
		}
		return Stream{URL: l.file(p), ContentType: kind, Direct: true, Session: session}, nil
	}
	media, err := l.started(ctx, ask)
	if err != nil {
		return Stream{}, err
	}
	return Stream{URL: media, ContentType: HLSType, Session: session}, nil
}

func (l *Library) file(p Part) string {
	return l.Base + p.Key + "?X-Plex-Token=" + url.QueryEscape(l.Token)
}

type asking struct {
	path, session, extra string
	local                bool
}

func (a asking) decision() operations.MakeDecisionRequest {
	first, yes := int64(0), components.BoolIntTrue
	hls, where := operations.QueryParamProtocolHls, operations.LocationWan
	if a.local {
		where = operations.LocationLan
	}
	return operations.MakeDecisionRequest{
		TranscodeType:           components.TranscodeTypeVideo,
		TranscodeSessionID:      &a.session,
		Path:                    &a.path,
		MediaIndex:              &first,
		PartIndex:               &first,
		Protocol:                &hls,
		DirectPlay:              &yes,
		DirectStream:            &yes,
		DirectStreamAudio:       &yes,
		FastSeek:                &yes,
		Copyts:                  &yes,
		Location:                &where,
		XPlexSessionIdentifier:  &a.session,
		XPlexClientProfileExtra: &a.extra,
	}
}

func (a asking) start() operations.StartTranscodeSessionRequest {
	first, yes := int64(0), components.BoolIntTrue
	hls := operations.StartTranscodeSessionQueryParamProtocolHls
	where := operations.StartTranscodeSessionQueryParamLocationWan
	if a.local {
		where = operations.StartTranscodeSessionQueryParamLocationLan
	}
	return operations.StartTranscodeSessionRequest{
		TranscodeType:           components.TranscodeTypeVideo,
		Extension:               operations.ExtensionM3u8,
		TranscodeSessionID:      &a.session,
		Path:                    &a.path,
		MediaIndex:              &first,
		PartIndex:               &first,
		Protocol:                &hls,
		DirectPlay:              &yes,
		DirectStream:            &yes,
		DirectStreamAudio:       &yes,
		FastSeek:                &yes,
		Copyts:                  &yes,
		Location:                &where,
		XPlexSessionIdentifier:  &a.session,
		XPlexClientProfileExtra: &a.extra,
	}
}

func (l *Library) started(ctx context.Context, a asking) (string, error) {
	res, err := l.api().Transcoder.StartTranscodeSession(ctx, a.start())
	if err != nil {
		return "", quiet("transcode start", err)
	}
	body := res.TwoHundredApplicationVndAppleMpegurlBinaryResponse
	if body == nil {
		return "", fmt.Errorf("plex: transcode start: HTTP %d, no playlist", res.StatusCode)
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, playlistMost))
	if err != nil {
		return "", fmt.Errorf("plex: transcode start: %w", err)
	}
	pl, err := playlist.Unmarshal(raw)
	if err != nil {
		return "", fmt.Errorf("plex: transcode start: %w", err)
	}
	mv, ok := pl.(*playlist.Multivariant)
	if !ok || len(mv.Variants) != 1 {
		return "", errors.New("plex: transcode start: not a single-variant playlist")
	}
	start, err := url.Parse(l.Base + startPath)
	if err != nil {
		return "", err
	}
	media, err := start.Parse(mv.Variants[0].URI)
	if err != nil {
		return "", fmt.Errorf("plex: transcode start: %w", err)
	}
	return media.String(), nil
}

func decided(
	d *components.MediaContainerWithDecision,
) (components.MediaContainerWithDecisionPart, error) {
	if d == nil || d.MediaContainer == nil {
		return components.MediaContainerWithDecisionPart{}, errors.New("plex: empty decision")
	}
	c := d.MediaContainer
	for _, md := range c.Metadata {
		for _, m := range md.Media {
			for _, p := range m.Part {
				if p.Decision != nil && *p.Decision != components.DecisionNone {
					return p, nil
				}
			}
		}
	}
	return components.MediaContainerWithDecisionPart{}, fmt.Errorf(
		"plex: %s",
		deref(c.GeneralDecisionText),
	)
}

func logDecision(it Item, d *components.MediaContainerWithDecision) {
	if d == nil || d.MediaContainer == nil {
		slog.Warn("plex transcode decision", "item", it.RatingKey, "reply", "empty")
		return
	}
	c := d.MediaContainer
	slog.Info("plex transcode decision", "item", it.RatingKey,
		"general", derefInt(c.GeneralDecisionCode), "general_text", deref(c.GeneralDecisionText),
		"direct_play", derefInt(c.DirectPlayDecisionCode),
		"direct_play_text", deref(c.DirectPlayDecisionText),
		"transcode", derefInt(c.TranscodeDecisionCode),
		"transcode_text", deref(c.TranscodeDecisionText))
}

func playsTrack(m Media) bool {
	return slices.Contains(directMusic, strings.ToLower(m.Container)) ||
		slices.Contains(trackAudio, strings.ToLower(m.Audio))
}

func trackType(m Media) string {
	switch strings.ToLower(m.Container) {
	case "flac":
		return "audio/flac"
	case "mp4", "m4a", "aac":
		return "audio/mp4"
	case "ogg", "opus":
		return "audio/ogg"
	}
	return "audio/mpeg"
}

type State string

const (
	Playing   State = "playing"
	Paused    State = "paused"
	Stopped   State = "stopped"
	Buffering State = "buffering"
)

func (l *Library) Report(
	ctx context.Context,
	it Item,
	st State,
	at, length time.Duration,
	session string,
) error {
	key := "/library/metadata/" + it.RatingKey
	state := operations.State(st)
	ms, total := at.Milliseconds(), length.Milliseconds()
	res, err := l.api().Timeline.Report(ctx, operations.ReportRequest{
		Key:                    &key,
		RatingKey:              &it.RatingKey,
		State:                  &state,
		Time:                   &ms,
		Duration:               &total,
		XPlexSessionIdentifier: &session,
	})
	if err != nil {
		return quiet("timeline", err)
	}
	return status(res.StatusCode, "timeline")
}

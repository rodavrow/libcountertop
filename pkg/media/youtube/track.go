package youtube

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/pion/opus"
	aacpcm "github.com/tphakala/go-aac/pcm"

	"github.com/ygelfand/libcountertop/pkg/media/hls"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/media/sponsorblock"
	"github.com/ygelfand/libcountertop/pkg/media/webm"
)

const (
	rate     = 48000
	channels = 2
)

// One Opus packet carries at most 120 ms per channel: 5760 samples at 48 kHz.
const maxFrame = 5760

const (
	wallFloor   = 20 * 365 * 24 * time.Hour
	rewindLeast = time.Minute
	liveSlack   = 15 * time.Second
	liveLead    = 10 * time.Second
)

type Options struct {
	reports  *watch
	At       time.Duration
	Skips    func(ctx context.Context) []sponsorblock.Segment
	Finished func()
	Skip     func(seg sponsorblock.Segment)
	Seek     func(to time.Duration)
}

type Track struct {
	info    Info
	r       *Resolver
	o       Options
	stream  io.ReadCloser
	packets demuxer
	sound   func(data []byte) ([]int16, error)

	mu       sync.Mutex
	ended    bool
	start    time.Duration
	produced int64
	skip     int
	left     []int16

	skips   []sponsorblock.Segment
	jumping bool

	live *hls.Stream
	base time.Time

	reports *watch
}

var errJumping = errors.New("youtube: jumping past a segment")

var (
	_ playback.Stream   = (*Track)(nil)
	_ playback.Pictures = (*Track)(nil)
	_ playback.Live     = (*Track)(nil)
	_ playback.Marked   = (*Track)(nil)
	_ playback.Seeker   = (*Track)(nil)
)

func Open(ctx context.Context, r *Resolver, id string, o Options) (*Track, error) {
	skips := make(chan []sponsorblock.Segment, 1)
	go func() {
		if o.Skips == nil {
			skips <- nil
			return
		}
		skips <- o.Skips(ctx)
	}()

	info, stream, reports, err := r.audio(ctx, id, o.reports)
	if l, ok := errors.AsType[*Live](err); ok {
		return openLive(ctx, r, l, o)
	}
	if err != nil {
		return nil, err
	}
	t, err := newTrack(r, info, stream, o)
	if err != nil {
		stream.Close()
		return nil, err
	}
	t.skips = <-skips
	t.reports = reports
	return t, nil
}

func WallClock(at time.Duration) bool { return at > wallFloor }

func openLive(ctx context.Context, r *Resolver, l *Live, o Options) (*Track, error) {
	var from time.Time
	if WallClock(o.At) {
		from = time.Unix(0, int64(o.At))
	}
	limits := r.limits()
	s, err := hls.Open(
		ctx,
		l.HLS,
		hls.Options{
			HTTP:      r.http,
			Ask:       ask,
			Tallest:   limits.Tallest,
			Fastest:   limits.Fastest,
			SoundOnly: r.target == nil,
			Lead:      liveLead,
			From:      from,
			Resample:  r.Resample,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("youtube: %s: %w", l.Info.ID, err)
	}
	t := &Track{info: l.Info, r: r, o: o, live: s, reports: o.reports}
	if t.reports == nil {
		t.reports = r.report(l.Info.ID)
	}
	if oldest, _, ok := s.Window(); ok {
		t.base = oldest
	}
	slog.Debug(
		"youtube live",
		"video",
		l.Info.ID,
		"from",
		from,
		"base",
		t.base,
		"size",
		fmt.Sprintf("%dx%d", s.Width, s.Height),
	)
	return t, nil
}

func newTrack(r *Resolver, info Info, stream io.ReadCloser, o Options) (*Track, error) {
	t := &Track{info: info, r: r, o: o, stream: stream, start: o.At}
	open := t.openOpus
	if info.Codec == codecAAC {
		open = t.openAAC
	}
	if err := open(); err != nil {
		return nil, fmt.Errorf("youtube: %s: %w", info.ID, err)
	}
	if err := t.cueUp(); err != nil {
		return nil, fmt.Errorf("youtube: %s: %w", info.ID, err)
	}
	return t, nil
}

func (t *Track) openOpus() error {
	demux := webm.NewReader(t.stream)
	tr, err := demux.Track()
	if err != nil {
		return err
	}
	if tr.Codec != "A_OPUS" {
		return fmt.Errorf("%s, not opus", tr.Codec)
	}
	dec, err := opus.NewDecoderWithOutput(rate, channels)
	if err != nil {
		return err
	}
	frame := make([]int16, maxFrame*channels)
	t.packets = webmAudio{Reader: demux, src: t.stream}
	t.sound = func(data []byte) ([]int16, error) {
		got, err := dec.DecodeToInt16(data, frame)
		if err != nil {
			return nil, err
		}
		return frame[:got*channels], nil
	}
	if t.start == 0 && len(tr.Private) >= 12 {
		t.skip = int(binary.LittleEndian.Uint16(tr.Private[10:12])) * channels
	}
	return nil
}

func (t *Track) openAAC() error {
	d, err := newDash(t.stream)
	if err != nil {
		return err
	}
	dec, err := aacpcm.NewRawDecoder(d.config)
	if err != nil {
		return fmt.Errorf("aac: %w", err)
	}
	resample := func(p []int16) []int16 { return p }
	if hz := dec.SampleRate(); hz != rate {
		if t.r == nil || t.r.Resample == nil {
			return fmt.Errorf("aac at %d Hz with no resampler", hz)
		}
		resample = t.r.Resample(hz)
	}
	from := dec.Channels()
	var buf []byte
	t.packets = d
	t.sound = func(data []byte) ([]int16, error) {
		out, _, err := dec.DecodeFrame(buf[:0], data)
		if err != nil {
			return nil, err
		}
		buf = out
		return resample(stereo(out, from)), nil
	}
	return nil
}

func (t *Track) cueUp() error {
	s, ok := t.stream.(interface{ From(int64) })
	if !ok || t.start <= 0 {
		return nil
	}
	off, cue, found, err := t.packets.cue(t.start)
	if err != nil {
		return err
	}
	slog.Debug(
		"youtube audio cue",
		"video",
		t.info.ID,
		"want",
		t.start,
		"cue",
		cue,
		"at",
		off,
		"found",
		found,
	)
	if found {
		s.From(off)
		t.packets.restart(off)
	}
	return nil
}

type webmAudio struct {
	*webm.Reader
	src io.Reader
}

func (w webmAudio) next() (packet, error) {
	f, err := w.Next()
	return packet{at: time.Duration(f.Time), data: f.Data}, err
}

func (w webmAudio) cue(at time.Duration) (int64, time.Duration, bool, error) {
	if err := w.Index(); err != nil {
		return 0, 0, false, err
	}
	off, cue, ok := w.Cue(int64(at))
	return off, time.Duration(cue), ok, nil
}

func (w webmAudio) restart(off int64) { w.Restart(w.src, off) }

func (t *Track) Info() Info { return t.info }

func (t *Track) Window() (oldest, edge time.Time, ok bool) {
	if t.live == nil {
		return time.Time{}, time.Time{}, false
	}
	return t.live.Window()
}

func (t *Track) Base() time.Time { return t.base }

func (t *Track) Read(ctx context.Context, pcm []int16) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if t.stream != nil {
		done := make(chan struct{})
		stop := context.AfterFunc(ctx, func() { t.stream.Close(); close(done) })
		defer func() {
			if !stop() {
				<-done
			}
		}()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.jumping {
		return 0, nil
	}
	if t.live != nil {
		return t.readLive(pcm)
	}

	n := 0
	for n < len(pcm) {
		if len(t.left) == 0 {
			if err := t.decode(); err != nil {
				if errors.Is(err, errJumping) {
					break
				}
				if errors.Is(err, io.EOF) {
					t.finish()
				}
				return n, err
			}
			continue
		}
		c := copy(pcm[n:], t.left)
		t.left = t.left[c:]
		n += c
	}
	t.produced += int64(n / channels)
	return n, nil
}

func (t *Track) readLive(pcm []int16) (int, error) {
	n, err := t.live.Read(pcm)
	t.produced += int64(n / channels)
	if errors.Is(err, io.EOF) {
		t.finish()
	}
	return n, err
}

func (t *Track) finish() {
	if t.ended {
		return
	}
	t.ended = true
	if t.o.Finished != nil {
		go t.o.Finished()
	}
}

func (t *Track) decode() error {
	for {
		f, err := t.packets.next()
		if err != nil {
			return err
		}
		at := f.at
		if at < t.start {
			continue
		}
		if s, ok := sponsorblock.Inside(t.skips, t.start, at); ok {
			t.jumping = true
			if t.o.Skip != nil {
				go t.o.Skip(s)
			}
			return errJumping
		}
		samples, err := t.sound(f.data)
		if err != nil {
			continue
		}
		if t.skip > 0 {
			drop := min(t.skip, len(samples))
			samples, t.skip = samples[drop:], t.skip-drop
		}
		if len(samples) > 0 {
			t.left = samples
			return nil
		}
	}
}

func (t *Track) Elapsed() time.Duration {
	start := t.Start()
	t.mu.Lock()
	defer t.mu.Unlock()
	return start + time.Duration(t.produced)*time.Second/rate
}

func (t *Track) Start() time.Duration {
	if t.live == nil || t.base.IsZero() {
		return t.start
	}
	began := t.live.Began()
	if began.IsZero() {
		return 0
	}
	return max(began.Sub(t.base), 0)
}

func (t *Track) Marks() []playback.Mark { return sponsorblock.Marks(t.skips) }

func (t *Track) Fast() {}

func (t *Track) Seek(to time.Duration) {
	if t.o.Seek == nil {
		return
	}
	if t.live == nil {
		go t.o.Seek(max(to, 0))
		return
	}
	if !t.base.IsZero() {
		go t.o.Seek(time.Duration(t.base.Add(max(to, 0)).UnixNano()))
	}
}

func (t *Track) Pictured() bool { return t.r != nil && t.r.target != nil }

func (t *Track) Pictures(ctx context.Context) (io.ReadCloser, time.Duration, error) {
	if !t.Pictured() {
		return nil, 0, nil
	}
	_, stream, err := t.r.Video(ctx, t.info.ID)
	return stream, t.start, err
}

func (t *Track) Live(context.Context) (playback.Picture, error) {
	if t.live == nil {
		return playback.Picture{}, nil
	}
	next := func(ctx context.Context) ([]byte, time.Duration, error) {
		f, err := t.live.Next(ctx)
		return f.Data, f.At + t.Start(), err
	}
	return playback.Picture{
		Codec:  playback.H264,
		Width:  t.live.Width,
		Height: t.live.Height,
		Next:   next,
	}, nil
}

func (t *Track) Showing() playback.Showing {
	s := playback.Showing{
		Title:  t.info.Title,
		Artist: t.info.Author,
		Art:    t.info.Thumbnail,
		Length: t.info.Duration,
	}
	if oldest, edge, ok := t.Window(); ok && edge.Sub(oldest) >= rewindLeast && !t.base.IsZero() {
		s.Length = edge.Sub(t.base)
		s.LiveWithin = liveLead + liveSlack
	}
	return s
}

func (t *Track) report(at time.Duration) {
	if t.reports != nil && t.live == nil {
		t.reports.report(at)
	}
}

func (t *Track) Close() {
	if t.live != nil {
		t.live.Close()
		return
	}
	if t.stream != nil {
		t.stream.Close()
	}
}

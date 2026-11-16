// Package videoplay plays a video from a URL on the local network when Home Assistant asks, with
// no cast receiver and no internet involved.
package videoplay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/media/cast"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/media/urlplayer"
)

type State string

const (
	Idle    State = "idle"
	Loading State = "loading"
	Playing State = "playing"
	Paused  State = "paused"
	Failed  State = "error"
)

const (
	Name          = "videoplay"
	positionEvery = 5 * time.Second
	maxURL        = 4096
	maxTitle      = 256
)

var errIdle = errors.New("videoplay: no video is playing")

type Options struct {
	// HTTP is the client to fetch with. Nil, the usual choice, builds one with no proxy that refuses
	// to connect anywhere Allowed does not.
	HTTP     *http.Client
	Output   func(ended func()) playback.Output
	Target   func() playback.Target
	Resample func(from int) func([]int16) []int16
	// Allowed says which addresses a video may come from; nil means Local.
	Allowed func(netip.Addr) bool
}

type Player struct {
	o      Options
	guard  *guard
	client *http.Client

	state    *esphome.TextSensor
	position *esphome.Sensor

	build sync.Once
	urls  atomic.Pointer[urlplayer.Player]

	mu  sync.Mutex
	now State
}

func New(o Options) *Player {
	if o.Output == nil {
		panic("videoplay: New needs an Output")
	}
	g := newGuard(o.Allowed)
	p := &Player{
		o:      o,
		guard:  g,
		client: g.client(o.HTTP),
		state: &esphome.TextSensor{
			Base: esphome.Base{
				ObjectID: "video_state",
				Name:     "Video state",
				Icon:     "mdi:television-play",
			},
		},
		position: &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: "video_position",
				Name:     "Video position",
				Icon:     "mdi:timer-play-outline",
			},
			Unit:        "s",
			DeviceClass: "duration",
			StateClass:  esphome.StateClassMeasurement,
		},
		now: Idle,
	}
	p.state.Set(string(Idle))
	p.position.Set(0)
	return p
}

func (p *Player) Name() string { return Name }

func (p *Player) Entities() []esphome.Entity { return []esphome.Entity{p.state, p.position} }

func (p *Player) Run(ctx context.Context) error {
	t := time.NewTicker(positionEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return p.Stop()
		case <-t.C:
			if p.State() == Playing {
				p.publish()
			}
		}
	}
}

func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now
}

func (p *Player) Actions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name:        "play_video",
			Description: "Play a video from a URL on the local network, full screen.",
			Args: []esphome.Arg{
				{
					Name:        "url",
					Type:        esphome.ArgString,
					Description: "An http or https URL whose host is on the local network.",
					Example:     "http://192.168.1.10:8099/video.mp4",
				},
				{Name: "title", Type: esphome.ArgString, Example: "Holiday"},
				{
					Name:        "start",
					Type:        esphome.ArgInt,
					Description: "Where to start, in seconds.",
					Example:     "0",
				},
			},
			Run: func(c esphome.Call) (any, error) {
				err := p.Play(context.Background(), c.String("url"), c.String("title"), c.Int("start"))
				return nil, err
			},
		},
		{
			Name:        "pause_video",
			Description: "Pause the video.",
			Run:         func(esphome.Call) (any, error) { return nil, p.Pause() },
		},
		{
			Name:        "resume_video",
			Description: "Resume the paused video.",
			Run:         func(esphome.Call) (any, error) { return nil, p.Resume() },
		},
		{
			Name:        "stop_video",
			Description: "Stop the video and close it.",
			Run:         func(esphome.Call) (any, error) { return nil, p.Stop() },
		},
		{
			Name:        "seek_video",
			Description: "Move the video to a position.",
			Args: []esphome.Arg{
				{Name: "seconds", Type: esphome.ArgInt, Description: "From the start.", Example: "90"},
			},
			Run: func(c esphome.Call) (any, error) { return nil, p.Seek(c.Int("seconds")) },
		},
	}
}

// Play checks the URL is local before anything is fetched, then replaces whatever this player
// shows with it.
func (p *Player) Play(ctx context.Context, raw, title string, start int) error {
	switch {
	case raw == "":
		return errors.New("videoplay: play_video needs a url")
	case len(raw) > maxURL:
		return fmt.Errorf("videoplay: the url is longer than %d bytes", maxURL)
	case utf8.RuneCountInString(title) > maxTitle:
		return fmt.Errorf("videoplay: the title is longer than %d characters", maxTitle)
	case start < 0:
		return errors.New("videoplay: start cannot be negative")
	}
	u, err := p.guard.parse(ctx, raw)
	if err != nil {
		return err
	}
	urls := p.player()
	at := time.Duration(start) * time.Second
	p.set(Loading)
	p.position.Set(float32(start))
	urls.Describe(&cast.Metadata{MetadataType: cast.MetadataMovie, Title: title})
	media := cast.Media{
		ContentID:   u.String(),
		ContentType: "video/mp4",
		StreamType:  cast.StreamBuffered,
	}
	if err := urls.Load(media, at, true); err != nil {
		p.set(Failed)
		return err
	}
	slog.Info("a video plays", "url", u.Redacted(), "title", title, "start", at)
	return nil
}

func (p *Player) Pause() error {
	urls, err := p.active()
	if err != nil {
		return err
	}
	if err := urls.Pause(); err != nil {
		return err
	}
	p.set(Paused)
	p.publish()
	return nil
}

func (p *Player) Resume() error {
	urls, err := p.active()
	if err != nil {
		return err
	}
	if err := urls.Play(); err != nil {
		return err
	}
	if urls.Ready() {
		p.set(Playing)
	} else {
		p.set(Loading)
	}
	return nil
}

func (p *Player) Seek(seconds int) error {
	if seconds < 0 {
		return errors.New("videoplay: seconds cannot be negative")
	}
	return p.seek(time.Duration(seconds) * time.Second)
}

func (p *Player) seek(to time.Duration) error {
	urls, err := p.active()
	if err != nil {
		return err
	}
	if err := urls.Seek(max(to, 0)); err != nil {
		return err
	}
	p.position.Set(float32(to.Seconds()))
	return nil
}

func (p *Player) Stop() error {
	if p.State() == Idle {
		return nil
	}
	p.stop()
	return nil
}

func (p *Player) player() *urlplayer.Player {
	p.build.Do(func() {
		p.urls.Store(urlplayer.New(urlplayer.Options{
			HTTP:     p.client,
			Output:   watched{Output: p.o.Output(p.ended), failed: p.refused},
			Resample: p.o.Resample,
			Screen:   p.o.Target != nil,
			Target:   p.o.Target,
			Remote:   remote{p},
			Ready:    p.ready,
		}))
	})
	return p.urls.Load()
}

func (p *Player) active() (*urlplayer.Player, error) {
	switch p.State() {
	case Idle, Failed:
		return nil, errIdle
	}
	return p.player(), nil
}

func (p *Player) set(s State) {
	p.mu.Lock()
	changed := p.now != s
	p.now = s
	p.mu.Unlock()
	if changed {
		p.state.Set(string(s))
	}
}

func (p *Player) publish() {
	if urls := p.urls.Load(); urls != nil {
		p.position.Set(float32(urls.Elapsed().Seconds()))
	}
}

func (p *Player) ready() {
	p.mu.Lock()
	loading := p.now == Loading
	if loading {
		p.now = Playing
	}
	p.mu.Unlock()
	if loading {
		p.state.Set(string(Playing))
	}
}

// stop lets go of the output, which takes the video off the screen.
func (p *Player) stop() { p.end(Idle) }

func (p *Player) end(s State) {
	if urls := p.urls.Load(); urls != nil {
		_ = urls.Stop()
		urls.Describe(nil)
	}
	p.set(s)
	p.position.Set(0)
}

func (p *Player) ended() {
	if p.State() != Idle {
		p.stop()
	}
}

// failed hears a video that stopped partway; running out of stream is the end, not a fault.
func (p *Player) failed(err error) {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		p.stop()
		return
	}
	slog.Warn("the video failed", "err", err)
	p.end(Failed)
}

func (p *Player) refused(err error) {
	slog.Warn("the video would not open", "err", err)
	p.end(Failed)
}

// watched hears the failures that reach the output rather than the source, such as a URL that
// would not open.
type watched struct {
	playback.Output
	failed func(error)
}

func (w watched) Failed(err error) {
	w.Output.Failed(err)
	w.failed(err)
}

// remote is how the on-screen controls, and anything that takes the output away, reach the player.
type remote struct{ p *Player }

func (r remote) Play() error                 { return r.p.Resume() }
func (r remote) Pause() error                { return r.p.Pause() }
func (r remote) Stop() error                 { return r.p.Stop() }
func (r remote) Failed(err error)            { r.p.failed(err) }
func (r remote) Finished()                   { r.p.stop() }
func (r remote) Seek(to time.Duration) error { return r.p.seek(to) }

var (
	_ urlplayer.Remote  = remote{}
	_ playback.Finisher = remote{}
	_ playback.Output   = watched{}
)

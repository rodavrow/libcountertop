package airplay

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"
	esphome "github.com/ygelfand/go-esphome-device"
	"golang.org/x/sys/unix"

	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/network/advertise"
	"github.com/ygelfand/libcountertop/pkg/runtime/payload"
	"github.com/ygelfand/libcountertop/pkg/runtime/service"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

var (
	errVersion  = errors.New("airplay: helper speaks another protocol version")
	errNoScreen = errors.New("airplay: screen size not known")
)

const (
	raopService    = "_raop._tcp"
	airplayService = "_airplay._tcp"
	mdnsDomain     = "local."
)

type Options struct {
	Settings  storage.Section[schema.AirPlay]
	Device    uint32
	Helper    string
	Binary    []byte
	Keyfile   string
	Name      func() string
	MAC       func() string
	Size      func() (width, height int)
	Largest   image.Point
	Output    func(ended func()) playback.Output
	Video     func() playback.Target
	Volume    Volume
	OpenPorts func()
	Latency   func() time.Duration
	Rate      int
	Channels  int
}

type Receiver struct {
	opts  Options
	knobs *Controls

	mu     sync.Mutex
	cancel context.CancelFunc
	live   *screening
	codes  func(client, code string) (release func())
	wg     sync.WaitGroup
}

func (r *Receiver) ShowCodes(show func(client, code string) (release func())) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes = show
}

func (r *Receiver) shower() func(client, code string) (release func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.codes
}

func NewReceiver(o Options) *Receiver {
	r := &Receiver{opts: o}
	r.knobs = &Controls{
		Table:  NewTable(),
		Device: o.Device,
		Read:   o.Settings.Read,
		Save:   r.save,
	}
	return r
}

func (r *Receiver) Name() string { return "airplay" }

func (r *Receiver) Entities() []esphome.Entity { return r.knobs.Entities() }

func (r *Receiver) Settings() *Controls { return r.knobs }

func (r *Receiver) Restore(c schema.AirPlay) {
	r.knobs.PublishFrom(c)
	if c.Enabled {
		r.Start()
	}
}

func (r *Receiver) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Go(func() { r.run(ctx) })
}

func (r *Receiver) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		r.wg.Wait()
	}
}

func (r *Receiver) SetEnabled(on bool) {
	if err := r.knobs.Change("airplay", setting.OnOff(on)); err != nil {
		slog.Error("saving a setting failed", "setting", "media.airplay", "err", err)
	}
}

func (r *Receiver) Resize() {
	r.mu.Lock()
	live := r.live
	r.mu.Unlock()
	if live != nil {
		live.resized()
	}
}

func (r *Receiver) save(s Setting, v string) error {
	if err := setting.Store(r.opts.Settings, s, v); err != nil {
		return err
	}
	if r.opts.Settings.Read().Enabled {
		r.Start()
	} else {
		r.Stop()
	}
	return nil
}

func (r *Receiver) run(ctx context.Context) {
	if err := payload.Install(r.opts.Helper, r.opts.Binary); err != nil {
		slog.Error("airplay helper not installed", "path", r.opts.Helper, "err", err)
		return
	}
	err := service.Keep(ctx, "airplay", func(ctx context.Context) error {
		for {
			if err := r.session(ctx); err != nil || ctx.Err() != nil {
				return err
			}
		}
	})
	if err != nil {
		slog.Error("airplay stopped", "err", err)
	}
}

func (r *Receiver) session(ctx context.Context) error {
	var w, h, sentW, sentH int
	if r.opts.Size != nil {
		w, h = r.opts.Size()
		if w <= 0 || h <= 0 {
			return errNoScreen
		}
		sentW, sentH = w, h
		if most := r.opts.Largest; most.X > 0 && most.Y > 0 {
			sentW, sentH = fit(w, h, most)
		}
	}
	if r.opts.OpenPorts != nil {
		r.opts.OpenPorts()
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("airplay: socketpair: %w", err)
	}
	unix.CloseOnExec(fds[0])
	unix.CloseOnExec(fds[1])
	if err := unix.SetNonblock(fds[0], true); err != nil {
		return fmt.Errorf(
			"airplay: socketpair: %w",
			errors.Join(err, unix.Close(fds[0]), unix.Close(fds[1])),
		)
	}
	ours := os.NewFile(uintptr(fds[0]), "airplay")
	theirs := os.NewFile(uintptr(fds[1]), "airplay-helper")
	defer ours.Close()

	proc, err := payload.Start(payload.Command{
		Path: r.opts.Helper,
		Args: []string{
			r.opts.Name(),
			r.opts.MAC(),
			r.opts.Keyfile,
			strconv.Itoa(sentW),
			strconv.Itoa(sentH),
			language(),
		},
		Files: []*os.File{theirs},
	})
	theirs.Close()
	if err != nil {
		return fmt.Errorf("airplay: %w", err)
	}
	defer func() {
		if err := proc.Wait(); err != nil {
			slog.Debug("airplay helper exit", "err", err)
		}
	}()
	live, restart := context.WithCancel(ctx)
	defer restart()
	stop := context.AfterFunc(live, func() { ours.Close() })
	defer stop()

	first, err := readMessage(ours)
	if err != nil {
		return fmt.Errorf("airplay: waiting for ready: %w", err)
	}
	if first.kind != msgReady {
		return fmt.Errorf("airplay: helper opened with %q", rune(first.kind))
	}
	rd, err := readyOf(first.payload)
	if err != nil {
		return err
	}
	if rd.version != version {
		return service.Permanent(fmt.Errorf("%w: %d", errVersion, rd.version))
	}
	slog.Info(
		"airplay ready",
		"name",
		r.opts.Name(),
		"port",
		rd.port,
		"size",
		fmt.Sprintf("%dx%d", sentW, sentH),
	)
	var announcing sync.WaitGroup
	defer announcing.Wait()
	shown, unshow := context.WithCancel(live)
	defer unshow()
	announcing.Go(func() { r.announce(shown, rd) })

	s := &screening{r: r, conn: ours, width: w, height: h, restart: restart}
	r.mu.Lock()
	r.live = s
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.live == s {
			r.live = nil
		}
		r.mu.Unlock()
	}()
	defer s.over()
	defer s.hideCode()
	if vol := r.opts.Volume; vol != nil {
		s.tellLevel()
		stop := vol.Listen(func(int) { s.tellLevel() })
		defer stop()
	}
	for {
		m, err := readMessage(ours)
		if err != nil {
			if live.Err() != nil {
				return nil
			}
			return fmt.Errorf("airplay: helper: %w", err)
		}
		s.handle(ctx, m)
	}
}

func (r *Receiver) announce(ctx context.Context, rd ready) {
	host := strings.ReplaceAll(
		r.opts.Name(),
		" ",
		"-",
	) + "-" + strings.ReplaceAll(
		r.opts.MAC(),
		":",
		"",
	)
	advertise.Advertise(ctx, "airplay", func(ips []net.IP) (func(), error) {
		addrs := advertise.Strings(ips)
		raop, err := zeroconf.RegisterProxy(
			rd.raop.instance, raopService, mdnsDomain, int(rd.port), host, addrs, rd.raop.text, nil,
		)
		if err != nil {
			return nil, fmt.Errorf("airplay: announcing raop: %w", err)
		}
		air, err := zeroconf.RegisterProxy(
			rd.airplay.instance,
			airplayService,
			mdnsDomain,
			int(rd.port),
			host,
			addrs,
			rd.airplay.text,
			nil,
		)
		if err != nil {
			raop.Shutdown()
			return nil, fmt.Errorf("airplay: announcing airplay: %w", err)
		}
		return func() {
			raop.Shutdown()
			air.Shutdown()
		}, nil
	})
}

type screening struct {
	r             *Receiver
	conn          *os.File
	width, height int
	restart       func()

	write  sync.Mutex
	client string

	mu      sync.Mutex
	playing source
	output  playback.Output
	video   *video
	code    func()
}

func (s *screening) showCode(code string) {
	show := s.r.shower()
	if show == nil {
		slog.Warn("airplay asked for a code with nowhere to show it", "client", s.client)
		return
	}
	slog.Info("airplay code", "client", s.client)
	release := show(s.client, code)
	s.mu.Lock()
	old := s.code
	s.code = release
	s.mu.Unlock()
	if old != nil {
		old()
	}
}

func (s *screening) hideCode() {
	s.mu.Lock()
	release := s.code
	s.code = nil
	s.mu.Unlock()
	if release != nil {
		release()
	}
}

type source interface {
	playback.Source
	hear(a audio)
	end()
}

func (s *screening) handle(ctx context.Context, m message) {
	switch m.kind {
	case msgClient:
		s.client = string(m.payload)
		slog.Info("airplay client", "name", s.client)
	case msgMirror:
		size, err := mirroringOf(m.payload)
		if _, known := size.picture(); err != nil || !known || s.r.opts.Size == nil {
			slog.Warn("airplay mirror refused", "codec", size.codec, "err", err)
			s.disconnect()
			return
		}
		slog.Info(
			"airplay mirroring",
			"client",
			s.client,
			"size",
			fmt.Sprintf("%dx%d", size.width, size.height),
			"codec",
			size.codec,
		)
		s.begin(size)
	case msgFrame:
		f, err := frameOf(m.payload)
		if err != nil {
			slog.Warn("airplay frame dropped", "err", err)
			return
		}
		s.mu.Lock()
		shown, ok := s.playing.(*mirror)
		s.mu.Unlock()
		if ok {
			shown.push(ctx, f)
		}
	case msgSound:
		snd := newSound(
			s.client,
			s.r.opts.Rate,
			s.r.opts.Channels,
			s.r.opts.Settings.Read().Audio,
			s.disconnect,
		)
		if s.r.opts.Latency != nil && snd.mode.Synced() {
			snd.latency = s.r.opts.Latency()
		}
		slog.Info("airplay audio", "client", s.client, "mode", snd.mode, "latency", snd.latency)
		s.play(snd)
	case msgAudio:
		a, err := audioOf(m.payload)
		if err != nil {
			slog.Warn("airplay audio dropped", "err", err)
			return
		}
		s.hear(a)
	case msgPlay:
		p, err := playOf(m.payload)
		if err != nil {
			slog.Warn("airplay video dropped", "err", err)
			return
		}
		s.stream(p)
	case msgScrub:
		if at, err := scrubOf(m.payload); err == nil {
			s.scrub(at)
		}
	case msgRate:
		if playing, err := rateOf(m.payload); err == nil {
			s.rate(playing)
		}
	case msgVolume:
		if db, err := dbOf(m.payload); err == nil {
			s.loudness(db)
		}
	case msgMute:
		if on, err := rateOf(m.payload); err == nil {
			s.muted(on)
		}
	case msgPin:
		s.showCode(string(m.payload))
	case msgPaired:
		slog.Info("airplay paired", "client", string(m.payload))
		s.hideCode()
	case msgStop:
		s.between()
	case msgEnded:
		if s.streaming() != nil {
			slog.Debug("airplay connection closed during a video", "client", s.client)
			return
		}
		slog.Info("airplay over", "client", s.client)
		s.over()
	}
}

func (s *screening) hear(a audio) {
	s.mu.Lock()
	playing := s.playing
	s.mu.Unlock()
	if playing != nil {
		playing.hear(a)
	}
}

func (s *screening) begin(size mirroring) {
	s.play(newMirror(s.client, size, s.r.opts.Rate, s.r.opts.Channels, s.disconnect))
}

func (s *screening) play(src source) {
	s.over()
	out := s.r.opts.Output(func() { s.ended(src) })
	s.mu.Lock()
	s.playing, s.output = src, out
	s.mu.Unlock()
	out.Play(src)
}

func (s *screening) ended(src source) {
	s.mu.Lock()
	current := s.playing == src
	s.mu.Unlock()
	src.end()
	if current {
		s.disconnect()
	}
}

func (s *screening) over() {
	s.mu.Lock()
	src, out, v := s.playing, s.output, s.video
	s.playing, s.output, s.video = nil, nil, nil
	s.mu.Unlock()
	if v != nil {
		v.stop()
	}
	if src != nil {
		src.end()
		out.Stop(src)
	}
	if src != nil || v != nil {
		s.resized()
	}
}

func (s *screening) resized() {
	s.mu.Lock()
	showing := s.playing != nil || s.video != nil
	s.mu.Unlock()
	if showing || s.r.opts.Size == nil {
		return
	}
	w, h := s.r.opts.Size()
	if w <= 0 || h <= 0 || (w == s.width && h == s.height) {
		return
	}
	slog.Info("airplay screen resized", "size", fmt.Sprintf("%dx%d", w, h))
	s.restart()
}

func (s *screening) disconnect() {
	s.write.Lock()
	defer s.write.Unlock()
	if err := writeMessage(s.conn, msgDisconnect, nil); err != nil {
		slog.Debug("airplay disconnect not sent", "err", err)
	}
}

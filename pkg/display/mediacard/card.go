package mediacard

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/media/nowplaying"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	config "github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

type Source = nowplaying.Source
type Opener = nowplaying.Opener
type Presenter interface{ Presents() bool }
type Player interface {
	Now() nowplaying.Now
	Holder() nowplaying.Source
	ResetSession()
}
type Dependencies struct {
	Player Player
	Idle   storage.Section[config.Idle]
	Media  storage.Section[config.Media]
	Device uint32
	Shell  *shell.Shell
}

func New(o Dependencies) *Card {
	return &Card{
		options:  o,
		page:     &Screen{},
		settings: NewSettings(o.Media, o.Device, o.Shell.Redraw),
	}
}

func (c *Card) Settings() *Settings             { return c.settings }
func (c *Card) Entities() []esphome.Entity      { return c.settings.Entities() }
func (c *Card) PlayerStart() config.PlayerStart { return c.options.Media.Read().Player }
func (c *Card) Mini() bool                      { return c.options.Media.Read().Mini }

func (c *Card) Background() bool    { return c.options.Media.Read().Background }
func (c *Card) Autoplay() bool      { return c.options.Media.Read().Autoplay }
func (c *Card) SetAutoplay(on bool) { c.change("autoplay", setting.OnOff(on)) }

func (c *Card) SetPlayerStart(p config.PlayerStart) { c.change("player", string(p)) }
func (c *Card) SetMini(on bool)                     { c.change("mini", setting.OnOff(on)) }
func (c *Card) SetBackground(on bool)               { c.change("background", setting.OnOff(on)) }

func (c *Card) change(name, value string) {
	if err := c.settings.Change(name, value); err != nil {
		slog.Warn("the player setting could not be saved", "setting", name, "err", err)
	}
}
func (c *Card) Page() *Screen  { return c.page }
func (c *Card) Began(s Source) { c.mu.Lock(); c.begun = s; c.mu.Unlock() }

func (c *Card) Close() { c.mu.Lock(); h := c.held; c.held = nil; c.mu.Unlock(); h.Release() }

type Screen struct {
	mu    sync.Mutex
	shown time.Duration
}

func (v *Screen) Covers() bool               { return true }
func (v *Screen) Docks() bool                { return true }
func (v *Screen) Shows(s config.Stream) bool { return s == config.StreamMedia }
func (v *Screen) Timeout() time.Duration     { return shell.SettingsTimeout }
func (v *Screen) Moved(elapsed time.Duration) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	second := elapsed / time.Second
	if second == v.shown {
		return false
	}
	v.shown = second
	return true
}

const (
	look = 250 * time.Millisecond
	gap  = 5 * time.Second
)

type Card struct {
	options  Dependencies
	settings *Settings
	page     *Screen
	mu       sync.Mutex
	held     *shell.Hold
	sounded  time.Time
	stalled  time.Time
	begun    Source
}

func (c *Card) Name() string { return "player" }

func (c *Card) Run(ctx context.Context) error {
	defer c.Close()
	tick := time.NewTicker(look)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		c.Follow()
		c.Tick()
	}
}

func (c *Card) Tick() {
	if c.options.Shell.Top() != c.page {
		return
	}
	now := c.options.Player.Now()
	if !now.Playing || now.Length <= 0 {
		return
	}
	if c.page.Moved(now.Elapsed) {
		c.options.Shell.Redraw()
	}
}

func (c *Card) Follow() {
	now := c.options.Player.Now()
	playing := now.Playing || now.Hold
	src := c.options.Player.Holder()

	idle := !now.Playing && (now.Paused || now.Hold)
	c.mu.Lock()
	begun := c.begun
	c.begun = nil
	if playing {
		c.sounded = time.Now()
	}
	switch {
	case !idle:
		c.stalled = time.Time{}
	case c.stalled.IsZero():
		c.stalled = time.Now()
	}
	stalled := time.Since(c.stalled)
	held, quiet := c.held, time.Since(c.sounded)
	if !playing && quiet > gap && begun == nil {
		c.options.Player.ResetSession()
	}
	c.mu.Unlock()

	switch {
	case begun != nil && begun == src:
		c.start(src)

	case !playing && held.Held() && quiet > gap:
		slog.Info("the player is done", "quiet", quiet.Round(time.Second))
		held.Keep(false)
	}

	if limit := c.options.Idle.Read().Media.After(); idle && src != nil && limit > 0 &&
		stalled > limit {
		slog.Info(
			"the player was left idle",
			"for",
			stalled.Round(time.Second),
			"by",
			fmt.Sprintf("%T", src),
		)
		c.mu.Lock()
		c.stalled = time.Time{}
		c.mu.Unlock()
		src.Stop()
	}
}

func (c *Card) Open() { c.open() }

func (c *Card) start(src Source) {
	if c.video() {
		slog.Info("the player opened", "by", fmt.Sprintf("%T", src), "as", "video")
		return
	}
	player := c.options.Media.Read().Player
	slog.Info("the player opened", "by", fmt.Sprintf("%T", src), "as", player)
	if player != config.PlayerNone {
		c.hold()
	}
}

func (c *Card) open() {
	if !c.video() {
		c.hold()
	}
}

func (c *Card) video() bool {
	o, ok := c.options.Player.Holder().(Opener)
	if !ok || !o.Open() {
		return false
	}
	c.mu.Lock()
	held := c.held
	c.held = nil
	c.mu.Unlock()
	held.Release()
	return true
}

func (c *Card) Left() {
	now := c.options.Player.Now()
	if !now.Playing && !now.Paused {
		return
	}
	if c.Background() {
		c.raise()
		return
	}
	if src := c.options.Player.Holder(); src != nil {
		src.Stop()
	}
}

func (c *Card) hold() {
	if p, ok := c.options.Shell.Top().(Presenter); ok && p.Presents() {
		return
	}
	c.raise()
}

func (c *Card) raise() {
	c.mu.Lock()
	held := c.held
	c.mu.Unlock()
	if held.Held() {
		return
	}
	hold := c.options.Shell.Hold(c.page)
	c.mu.Lock()
	c.held = hold
	c.mu.Unlock()
}

package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ygelfand/libcountertop/pkg/homeassistant"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/states"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

const (
	fetchWait    = 10 * time.Second
	stale        = 5 * time.Minute
	eventsAction = "calendar.get_events"
	createAction = "calendar.create_event"
	eventsShape  = "{{ response | to_json }}"
	dayLayout    = "2006-01-02"
	wireLayout   = "2006-01-02 15:04:05"
	features     = "supported_features"
	canCreate    = 1
)

var ErrReadOnly = errors.New("calendar: this calendar does not take new events")

type Event struct {
	Calendar    string
	Summary     string
	Location    string
	Description string
	Start       time.Time
	End         time.Time
	AllDay      bool
}

func (e Event) Overlaps(from, to time.Time) bool {
	end := e.End
	if !end.After(e.Start) {
		end = e.Start.Add(time.Nanosecond)
	}
	return e.Start.Before(to) && end.After(from)
}

type Draft struct {
	Calendar    string
	Summary     string
	Location    string
	Description string
	Start       time.Time
	End         time.Time
	AllDay      bool
}

type Options struct {
	Controller *homeassistant.Controller
	States     *states.States
	Settings   storage.Section[schema.Calendar]
	Redraw     func()
	DeviceID   uint32
}

type window struct{ from, to time.Time }

func (w window) covers(from, to time.Time) bool {
	return !w.from.IsZero() && !from.Before(w.from) && !to.After(w.to)
}

type Calendar struct {
	opts  Options
	knobs *setting.Controls[schema.Calendar]

	mu       sync.Mutex
	offered  []homeassistant.Entity
	failed   error
	fetching bool
	fetched  bool

	following string
	followed  map[string]bool
	events    []Event
	have      window
	want      window
	loaded    bool
	problem   error
	asked     time.Time
	asking    bool
}

func New(opts Options) *Calendar {
	c := &Calendar{opts: opts, followed: map[string]bool{}}
	c.knobs = &setting.Controls[schema.Calendar]{
		Table:  NewTable(),
		Device: opts.DeviceID,
		Read:   opts.Settings.Read,
		Save:   c.store,
	}
	return c
}

func (c *Calendar) Offered() ([]homeassistant.Entity, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offered, c.failed, c.fetched
}

func (c *Calendar) Fetch() {
	c.mu.Lock()
	if c.fetching {
		c.mu.Unlock()
		return
	}
	c.fetching = true
	c.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), fetchWait)
		defer cancel()
		list, err := c.opts.Controller.Entities(
			ctx,
			homeassistant.Filter{Domains: []string{"calendar"}, Attributes: []string{features}},
		)
		c.mu.Lock()
		c.offered, c.failed, c.fetching, c.fetched = list, err, false, true
		c.mu.Unlock()
		if err != nil {
			slog.Info("calendars", "err", err)
		}
		c.opts.Redraw()
	}()
}

func (c *Calendar) Read() schema.Calendar { return c.opts.Settings.Read() }

func (c *Calendar) Chosen() []string { return c.Read().List() }

func (c *Calendar) Title(entity string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.offered {
		if e.ID == entity && e.Name != "" {
			return e.Name
		}
	}
	return entity
}

func (c *Calendar) writable(entity string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.offered {
		if e.ID == entity {
			n, err := strconv.Atoi(e.Attributes[features])
			return err == nil && n&canCreate != 0
		}
	}
	return false
}

func (c *Calendar) Writable() []string {
	var out []string
	for _, e := range c.Chosen() {
		if c.writable(e) {
			out = append(out, e)
		}
	}
	return out
}

func (c *Calendar) Color(entity string) (string, int) { return c.Read().Color(entity) }

func (c *Calendar) Toggle(entity string) {
	list := c.Chosen()
	if i := slices.Index(list, entity); i >= 0 {
		list = slices.Delete(list, i, i+1)
	} else {
		list = append(list, entity)
	}
	c.write("calendars", c.writer().Entities(list))
}

func (c *Calendar) SetColor(entity, hex string) {
	c.write("calendar color", c.writer().Color(entity, hex))
}

func (c *Calendar) SetOpening(v schema.CalendarOpening) { c.change("opening", string(v)) }

func (c *Calendar) SetWeekStart(v schema.CalendarWeekStart) { c.change("week_start", string(v)) }

func (c *Calendar) SetWeather(on bool) { c.change("weather", setting.OnOff(on)) }

func (c *Calendar) SetSaver(on bool) { c.change("saver", setting.OnOff(on)) }

func (c *Calendar) change(name, v string) {
	if err := c.knobs.Change(name, v); err != nil {
		slog.Error("saving the calendar setting failed", "setting", name, "err", err)
	}
}

func (c *Calendar) SetView(v schema.CalendarView) {
	if err := c.writer().View(v); err != nil {
		slog.Error("saving the calendar view failed", "err", err)
	}
}

func (c *Calendar) writer() schema.CalendarWriter { return schema.NewCalendarWriter(c.opts.Settings) }

func (c *Calendar) write(what string, err error) {
	if err != nil {
		slog.Error("saving the setting failed", "what", what, "err", err)
	}
	c.changed()
}

func (c *Calendar) changed() {
	c.knobs.Publish()
	c.mu.Lock()
	c.asked = time.Time{}
	c.mu.Unlock()
	c.opts.Redraw()
}

func (c *Calendar) Events(from, to time.Time) ([]Event, error, bool) {
	chosen := c.Chosen()
	if len(chosen) == 0 || !to.After(from) {
		return nil, nil, false
	}
	key := strings.Join(chosen, ",")
	c.mu.Lock()
	if c.following != key {
		c.following, c.events, c.have, c.loaded, c.problem, c.asked = key, nil, window{}, false, nil, time.Time{}
	}
	var follow []string
	for _, e := range chosen {
		if !c.followed[e] {
			c.followed[e] = true
			follow = append(follow, e)
		}
	}
	covered := c.have.covers(from, to)
	if !covered {
		span := to.Sub(from)
		c.want = window{from.Add(-span), to.Add(span)}
	}
	due := !c.asking && (!covered || time.Since(c.asked) > stale)
	var events []Event
	for _, e := range c.events {
		if e.Overlaps(from, to) {
			events = append(events, e)
		}
	}
	problem, loaded := c.problem, c.loaded && covered
	c.mu.Unlock()
	for _, e := range follow {
		c.opts.States.Follow(e, "").Changed.Listen(func(string) { c.Refresh() })
	}
	if due {
		c.Refresh()
	}
	return events, problem, loaded
}

func (c *Calendar) Refresh() {
	chosen := c.Chosen()
	if len(chosen) == 0 {
		return
	}
	c.mu.Lock()
	if c.asking || c.want.from.IsZero() {
		c.mu.Unlock()
		return
	}
	c.asking, c.asked = true, time.Now()
	key, want := c.following, c.want
	c.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), fetchWait)
		defer cancel()
		raw, err := c.opts.Controller.Ask(ctx, eventsAction, map[string]string{
			"entity_id":       strings.Join(chosen, ","),
			"start_date_time": want.from.Format(wireLayout),
			"end_date_time":   want.to.Format(wireLayout),
		}, nil, eventsShape)
		var events []Event
		if err == nil {
			events, err = parse(raw, want.from.Location())
		}
		c.mu.Lock()
		c.asking = false
		again := false
		if c.following == key {
			if err == nil {
				c.events, c.have = events, want
			}
			c.problem, c.loaded = err, true
			again = c.want != want
		}
		c.mu.Unlock()
		if err != nil {
			slog.Info("calendar events", "err", err)
		}
		if again {
			c.Refresh()
		}
		c.opts.Redraw()
	}()
}

func (c *Calendar) Create(ctx context.Context, d Draft) error {
	if !c.writable(d.Calendar) {
		return ErrReadOnly
	}
	data := map[string]string{"entity_id": d.Calendar, "summary": strings.TrimSpace(d.Summary)}
	if l := strings.TrimSpace(d.Location); l != "" {
		data["location"] = l
	}
	if desc := strings.TrimSpace(d.Description); desc != "" {
		data["description"] = desc
	}
	if d.AllDay {
		end := d.End
		if !end.After(d.Start) {
			end = d.Start.AddDate(0, 0, 1)
		}
		data["start_date"], data["end_date"] = d.Start.Format(dayLayout), end.Format(dayLayout)
	} else {
		data["start_date_time"], data["end_date_time"] = d.Start.Format(
			wireLayout,
		), d.End.Format(
			wireLayout,
		)
	}
	if err := c.opts.Controller.Call(ctx, createAction, data); err != nil {
		return fmt.Errorf("calendar: adding the event: %w", err)
	}
	c.mu.Lock()
	c.asked = time.Time{}
	c.have = window{}
	c.mu.Unlock()
	c.Refresh()
	return nil
}

type wire struct {
	Start       string `json:"start"`
	End         string `json:"end"`
	Summary     string `json:"summary"`
	Location    string `json:"location"`
	Description string `json:"description"`
}

func parse(raw json.RawMessage, loc *time.Location) ([]Event, error) {
	var lists map[string]struct {
		Events []wire `json:"events"`
	}
	if err := json.Unmarshal(raw, &lists); err != nil {
		return nil, fmt.Errorf("calendar: the events: %w", err)
	}
	var out []Event
	for entity, list := range lists {
		for _, w := range list.Events {
			start, allDay, err := when(w.Start, loc)
			if err != nil {
				continue
			}
			end, _, err := when(w.End, loc)
			if err != nil {
				end = start
			}
			out = append(out, Event{
				Calendar:    entity,
				Summary:     w.Summary,
				Location:    w.Location,
				Description: w.Description,
				Start:       start,
				End:         end,
				AllDay:      allDay,
			})
		}
	}
	slices.SortFunc(out, func(a, b Event) int {
		if a.AllDay != b.AllDay && a.Start.Equal(b.Start) {
			if a.AllDay {
				return -1
			}
			return 1
		}
		return a.Start.Compare(b.Start)
	})
	return out, nil
}

func when(s string, loc *time.Location) (time.Time, bool, error) {
	if t, err := time.ParseInLocation(dayLayout, s, loc); err == nil {
		return t, true, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	return t.In(loc), false, err
}

func (c *Calendar) store(s Setting, v string) error {
	if err := setting.Store(c.opts.Settings, s, v); err != nil {
		return err
	}
	c.changed()
	return nil
}

package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/homeassistant"
	"github.com/ygelfand/libcountertop/pkg/say"
)

const (
	attrFeatures   = "supported_features"
	forecastAction = "weather.get_forecasts"
	forecastShape  = "{{ response | to_json }}"
	forecastStale  = 30 * time.Minute
	middayHour     = 13
)

type Kind string

const (
	KindDaily  Kind = "daily"
	KindTwice  Kind = "twice_daily"
	KindHourly Kind = "hourly"
)

func (k Kind) Label() string { return say.T("weather.forecast." + string(k)) }

var kindFlags = []struct {
	kind Kind
	flag int
}{
	{KindDaily, 1},
	{KindTwice, 4},
	{KindHourly, 2},
}

func Kinds(e homeassistant.Entity) []Kind {
	n, err := strconv.Atoi(e.Attributes[attrFeatures])
	if err != nil {
		return nil
	}
	var out []Kind
	for _, k := range kindFlags {
		if n&k.flag != 0 {
			out = append(out, k.kind)
		}
	}
	return out
}

func rank(e homeassistant.Entity) int {
	kinds := Kinds(e)
	if len(kinds) == 0 {
		return 0
	}
	for i, k := range kindFlags {
		if k.kind == kinds[0] {
			return len(kindFlags) - i
		}
	}
	return 0
}

func bestForecast(list []homeassistant.Entity, main string) string {
	best, score := "", 0
	for _, e := range list {
		s := rank(e)
		if s > score || (s == score && s > 0 && e.ID == main) {
			best, score = e.ID, s
		}
	}
	return best
}

type Day struct {
	Date      time.Time
	Condition string
	High      *float64
	Low       *float64
}

type daily struct {
	entity string
	days   []Day
	at     time.Time
	asking bool
}

func (w *Weather) ForecastEntity() string {
	cfg := w.opts.Settings.Read()
	if e := strings.TrimSpace(cfg.Forecast); e != "" {
		return e
	}
	return strings.TrimSpace(cfg.Entity)
}

func (w *Weather) kind(entity string) (Kind, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, e := range w.offered {
		if e.ID == entity {
			if kinds := Kinds(e); len(kinds) > 0 {
				return kinds[0], true
			}
			return "", false
		}
	}
	return "", false
}

func (w *Weather) Daily() []Day {
	entity := w.ForecastEntity()
	if entity == "" || !w.opts.Home.Read().Enabled ||
		w.opts.Controller.Access() != homeassistant.Allowed {
		return nil
	}
	if _, _, fetched := w.Offered(); !fetched {
		w.Fetch()
		return nil
	}
	kind, ok := w.kind(entity)
	if !ok {
		return nil
	}
	w.mu.Lock()
	if w.forecast.entity != entity {
		w.forecast = daily{entity: entity}
	}
	days := slices.Clone(w.forecast.days)
	due := !w.forecast.asking && time.Since(w.forecast.at) > forecastStale
	if due {
		w.forecast.asking = true
	}
	w.mu.Unlock()
	if due {
		go w.fetchDaily(entity, kind)
	}
	return days
}

func (w *Weather) fetchDaily(entity string, kind Kind) {
	ctx, cancel := context.WithTimeout(context.Background(), fetchWait)
	defer cancel()
	raw, err := w.opts.Controller.Ask(ctx, forecastAction, map[string]string{
		"entity_id": entity,
		"type":      string(kind),
	}, nil, forecastShape)
	var days []Day
	if err == nil {
		days, err = parseForecast(raw, entity, kind, time.Local)
	}
	w.mu.Lock()
	if w.forecast.entity == entity {
		w.forecast.asking, w.forecast.at = false, time.Now()
		if err == nil {
			w.forecast.days = days
		}
	}
	w.mu.Unlock()
	if err != nil {
		slog.Info("weather forecast", "entity", entity, "kind", kind, "err", err)
		return
	}
	w.opts.Redraw()
}

type entry struct {
	at        time.Time
	condition string
	temp      *float64
	low       *float64
	daytime   *bool
}

func parseForecast(
	raw json.RawMessage,
	entity string,
	kind Kind,
	loc *time.Location,
) ([]Day, error) {
	var all map[string]struct {
		Forecast []struct {
			Datetime    string   `json:"datetime"`
			Condition   string   `json:"condition"`
			Temperature *float64 `json:"temperature"`
			Low         *float64 `json:"templow"`
			Daytime     *bool    `json:"is_daytime"`
		} `json:"forecast"`
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("weather: the forecast: %w", err)
	}
	var entries []entry
	for _, f := range all[entity].Forecast {
		t, err := time.Parse(time.RFC3339, f.Datetime)
		if err != nil {
			continue
		}
		entries = append(entries, entry{
			at: t.In(
				loc,
			),
			condition: f.Condition,
			temp:      f.Temperature,
			low:       f.Low,
			daytime:   f.Daytime,
		})
	}
	switch kind {
	case KindTwice:
		return foldTwice(entries), nil
	case KindHourly:
		return foldHourly(entries), nil
	}
	out := make([]Day, 0, len(entries))
	for _, e := range entries {
		out = append(out, Day{Date: dateOf(e.at), Condition: e.condition, High: e.temp, Low: e.low})
	}
	return out, nil
}

func dateOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func group(entries []entry) ([]time.Time, map[time.Time][]entry) {
	var order []time.Time
	by := map[time.Time][]entry{}
	for _, e := range entries {
		d := dateOf(e.at)
		if _, ok := by[d]; !ok {
			order = append(order, d)
		}
		by[d] = append(by[d], e)
	}
	return order, by
}

func foldTwice(entries []entry) []Day {
	order, by := group(entries)
	out := make([]Day, 0, len(order))
	for _, d := range order {
		day := Day{Date: d}
		for _, e := range by[d] {
			night := e.daytime != nil && !*e.daytime
			switch {
			case night:
				if day.Condition == "" {
					day.Condition = e.condition
				}
				day.Low = e.temp
				if e.low != nil {
					day.Low = e.low
				}
			default:
				day.Condition, day.High = e.condition, e.temp
			}
		}
		out = append(out, day)
	}
	return out
}

func foldHourly(entries []entry) []Day {
	order, by := group(entries)
	out := make([]Day, 0, len(order))
	for _, d := range order {
		day := Day{Date: d}
		closest := -1
		for _, e := range by[d] {
			if e.temp != nil {
				t := *e.temp
				if day.High == nil || t > *day.High {
					day.High = &t
				}
				if day.Low == nil || t < *day.Low {
					low := t
					day.Low = &low
				}
			}
			gap := abs(e.at.Hour() - middayHour)
			if closest < 0 || gap < closest {
				closest, day.Condition = gap, e.condition
			}
		}
		out = append(out, day)
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

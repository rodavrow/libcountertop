package schema

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

const CalendarColors = 8

type Calendar struct {
	Entities string            `json:"entities"`
	Colors   map[string]string `json:"colors,omitempty"`
	Weather  bool              `json:"weather"`
	Opening  CalendarOpening   `json:"opening"`
	View     CalendarView      `json:"view,omitempty"`
	Saver    bool              `json:"saver"`
}

type CalendarView string

const (
	CalendarDay   CalendarView = "day"
	CalendarWeek  CalendarView = "week"
	CalendarMonth CalendarView = "month"
)

func CalendarViews() []CalendarView {
	return []CalendarView{CalendarDay, CalendarWeek, CalendarMonth}
}

func (v CalendarView) Label() string { return say.T("calendar.view." + string(v)) }

type CalendarOpening string

const (
	OpenLast  CalendarOpening = "last"
	OpenDay   CalendarOpening = "day"
	OpenWeek  CalendarOpening = "week"
	OpenMonth CalendarOpening = "month"
)

func CalendarOpenings() []CalendarOpening {
	return []CalendarOpening{OpenLast, OpenDay, OpenWeek, OpenMonth}
}

func (o CalendarOpening) Label() string { return say.T("calendar.opening." + string(o)) }

func DefaultCalendar() Calendar {
	return Calendar{Weather: true, Opening: OpenLast, View: CalendarMonth}
}

func (c Calendar) List() []string {
	var out []string
	for e := range strings.SplitSeq(c.Entities, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func (c Calendar) Has(entity string) bool { return slices.Contains(c.List(), entity) }

func (c Calendar) Opens() CalendarView {
	switch c.Opening {
	case OpenDay:
		return CalendarDay
	case OpenWeek:
		return CalendarWeek
	case OpenMonth:
		return CalendarMonth
	}
	if slices.Contains(CalendarViews(), c.View) {
		return c.View
	}
	return CalendarMonth
}

func HexColor(v string) bool {
	if len(v) != 7 || v[0] != '#' {
		return false
	}
	for _, r := range v[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func (c Calendar) Color(entity string) (string, int) {
	if v := c.Colors[entity]; HexColor(v) {
		return v, 0
	}
	slot := 0
	for _, e := range c.List() {
		if HexColor(c.Colors[e]) {
			continue
		}
		slot++
		if e == entity {
			return "", (slot-1)%CalendarColors + 1
		}
	}
	return "", slot%CalendarColors + 1
}

func ParseColors(v string) (map[string]string, error) {
	pairs, err := ParsePairs(v)
	if err != nil {
		return nil, err
	}
	for k, s := range pairs {
		if !HexColor(s) {
			return nil, fmt.Errorf("%s takes a #rrggbb color, not %q", k, s)
		}
	}
	return pairs, nil
}

func FormatPairs(m map[string]string) string {
	keys := slices.Sorted(maps.Keys(m))
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

func ParsePairs(v string) (map[string]string, error) {
	out := map[string]string{}
	for part := range strings.SplitSeq(v, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		k, val, ok := strings.Cut(part, "=")
		k, val = strings.TrimSpace(k), strings.TrimSpace(val)
		if !ok || k == "" || val == "" {
			return nil, fmt.Errorf("takes entity=value pairs, not %q", part)
		}
		out[k] = val
	}
	return out, nil
}

type CalendarWriter struct{ s storage.Section[Calendar] }

func NewCalendarWriter(s storage.Section[Calendar]) CalendarWriter {
	return CalendarWriter{s: s}
}

func (w CalendarWriter) Entities(list []string) error {
	return w.s.Update(func(c *Calendar) { c.Entities = strings.Join(list, ",") })
}

func (w CalendarWriter) Color(entity, hex string) error {
	return w.s.Update(func(c *Calendar) {
		colors := maps.Clone(c.Colors)
		if colors == nil {
			colors = map[string]string{}
		}
		if HexColor(hex) {
			colors[entity] = strings.ToLower(hex)
		} else {
			delete(colors, entity)
		}
		c.Colors = colors
	})
}

func (w CalendarWriter) View(v CalendarView) error {
	return w.s.Update(func(c *Calendar) { c.View = v })
}

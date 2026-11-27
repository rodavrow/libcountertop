package schema

import (
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/say"
)

func TestCalendarSlotsSkipPickedColors(t *testing.T) {
	c := Calendar{
		Entities: "calendar.a,calendar.b,calendar.c",
		Colors:   map[string]string{"calendar.b": "#3a7bd5"},
	}
	if hex, slot := c.Color("calendar.a"); hex != "" || slot != 1 {
		t.Errorf("calendar.a = %q, %d, want slot 1", hex, slot)
	}
	if hex, slot := c.Color("calendar.b"); hex != "#3a7bd5" || slot != 0 {
		t.Errorf("calendar.b = %q, %d, want #3a7bd5", hex, slot)
	}
	if hex, slot := c.Color("calendar.c"); hex != "" || slot != 2 {
		t.Errorf("calendar.c = %q, %d, want slot 2", hex, slot)
	}
}

func TestCalendarColorsTakeOnlyHex(t *testing.T) {
	if _, err := ParseColors("calendar.a=3"); err == nil {
		t.Error("calendar.a=3 parsed")
	}
	got, err := ParseColors("calendar.a=#3A7BD5, calendar.b=#000000")
	if err != nil || got["calendar.a"] != "#3A7BD5" || got["calendar.b"] != "#000000" {
		t.Errorf("ParseColors = %v, %v", got, err)
	}
}

func TestCalendarPairsRoundTrip(t *testing.T) {
	in := map[string]string{"calendar.b": "#222222", "calendar.a": "#111111"}
	text := FormatPairs(in)
	if text != "calendar.a=#111111,calendar.b=#222222" {
		t.Errorf("FormatPairs = %q", text)
	}
	out, err := ParsePairs(text)
	if err != nil || len(out) != 2 || out["calendar.b"] != "#222222" {
		t.Errorf("ParsePairs = %v, %v", out, err)
	}
	if _, err := ParsePairs("calendar.a"); err == nil {
		t.Error("a pair without = parsed")
	}
}

func TestCalendarOpensOnTheChosenView(t *testing.T) {
	for _, c := range []struct {
		cfg  Calendar
		want CalendarView
	}{
		{Calendar{Opening: OpenWeek, View: CalendarDay}, CalendarWeek},
		{Calendar{Opening: OpenLast, View: CalendarDay}, CalendarDay},
		{Calendar{Opening: OpenLast}, CalendarMonth},
	} {
		if got := c.cfg.Opens(); got != c.want {
			t.Errorf("%+v opens %s, want %s", c.cfg, got, c.want)
		}
	}
}

func TestCalendarWeekStartsOnTheChosenDay(t *testing.T) {
	defer say.Use("en")
	for _, c := range []struct {
		lang  string
		start CalendarWeekStart
		want  time.Weekday
	}{
		{"en", "", time.Sunday},
		{"en", WeekStartAuto, time.Sunday},
		{"en", WeekStartMonday, time.Monday},
		{"de", WeekStartAuto, time.Monday},
		{"de", WeekStartSunday, time.Sunday},
	} {
		say.Use(c.lang)
		if got := (Calendar{WeekStart: c.start}).FirstWeekday(); got != c.want {
			t.Errorf("%s with week start %q starts on %s, want %s", c.lang, c.start, got, c.want)
		}
	}
}

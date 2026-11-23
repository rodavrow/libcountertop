package agenda

import (
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/homeassistant/calendar"
)

func at(day, hour, minute int) time.Time {
	return time.Date(2026, time.October, day, hour, minute, 0, 0, time.UTC)
}

func TestTheMonthGridStartsOnTheWeekStart(t *testing.T) {
	from, weeks := monthGrid(at(18, 0, 0), time.Sunday)
	if !from.Equal(at(27, 0, 0).AddDate(0, -1, 0)) || weeks != 5 {
		t.Errorf("October 2026 from Sunday = %s, %d weeks", from.Format("Jan 2"), weeks)
	}
	from, weeks = monthGrid(at(18, 0, 0), time.Monday)
	if !from.Equal(at(28, 0, 0).AddDate(0, -1, 0)) || weeks != 5 {
		t.Errorf("October 2026 from Monday = %s, %d weeks", from.Format("Jan 2"), weeks)
	}
	if _, weeks := monthGrid(
		time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
		time.Sunday,
	); weeks != 6 {
		t.Errorf("August 2026 from Sunday has %d weeks, want 6", weeks)
	}
}

func TestBarsStackAndCarryOver(t *testing.T) {
	week := at(25, 0, 0)
	events := []calendar.Event{
		{Summary: "trash", Start: at(28, 0, 0), End: at(29, 0, 0), AllDay: true},
		{
			Summary: "vacation",
			Start:   at(29, 0, 0),
			End:     at(25, 0, 0).AddDate(0, 0, 9),
			AllDay:  true,
		},
		{Summary: "trip", Start: at(27, 18, 0), End: at(29, 9, 0)},
		{Summary: "standup", Start: at(28, 9, 0), End: at(28, 9, 30)},
	}
	placed := bars(events, week, 7)
	if len(placed) != 3 {
		t.Fatalf("placed %d bars, want 3 (the standup is not a bar)", len(placed))
	}
	byName := map[string]bar{}
	for _, b := range placed {
		byName[b.event.Summary] = b
	}
	if v := byName["vacation"]; v.from != 4 || v.to != 6 || !v.after || v.before {
		t.Errorf("vacation = %+v", v)
	}
	if trip, trash := byName["trip"], byName["trash"]; trip.lane == trash.lane {
		t.Errorf("trip and trash share lane %d", trip.lane)
	}
	if n := lanes(placed, 3); n != 2 {
		t.Errorf("Wednesday has %d lanes, want 2", n)
	}
}

func TestOverlappingEventsSitSideBySide(t *testing.T) {
	got := columns([]calendar.Event{
		{Summary: "a", Start: at(20, 9, 0), End: at(20, 10, 0)},
		{Summary: "b", Start: at(20, 9, 30), End: at(20, 11, 0)},
		{Summary: "c", Start: at(20, 10, 0), End: at(20, 10, 30)},
		{Summary: "d", Start: at(20, 15, 0), End: at(20, 16, 0)},
	})
	want := map[string][2]int{"a": {0, 2}, "b": {1, 2}, "c": {0, 2}, "d": {0, 1}}
	for _, b := range got {
		if w := want[b.event.Summary]; b.col != w[0] || b.cols != w[1] {
			t.Errorf(
				"%s at column %d of %d, want %d of %d",
				b.event.Summary,
				b.col,
				b.cols,
				w[0],
				w[1],
			)
		}
	}
}

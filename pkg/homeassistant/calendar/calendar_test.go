package calendar

import (
	"testing"
	"time"
)

func TestEventsAreListedFromTheirCalendarsResponse(t *testing.T) {
	if got := eventsListing(
		`calendar.o'brien`,
	).Source; got != `response["calendar.o'brien"].events` {
		t.Errorf("source %s", got)
	}
}

func TestParseKeepsEachEventsCalendarAndOrder(t *testing.T) {
	loc := time.UTC
	got := parse([]wire{
		{
			Calendar: "calendar.work",
			Start:    "2026-11-27T10:00:00-07:00",
			End:      "2026-11-27T11:00:00-07:00",
			Summary:  "standup",
		},
		{Calendar: "calendar.home", Start: "2026-11-27", End: "2026-11-28", Summary: "holiday"},
		{Calendar: "calendar.home", Start: "not a time", Summary: "dropped"},
		{
			Calendar:    "calendar.home",
			Start:       "2026-11-26T09:00:00-07:00",
			End:         "",
			Summary:     "no end",
			Description: "notes",
		},
	}, loc)
	if len(got) != 3 {
		t.Fatalf("parsed %d events, want 3: %+v", len(got), got)
	}
	want := []struct {
		calendar, summary string
		allDay            bool
	}{
		{"calendar.home", "no end", false},
		{"calendar.home", "holiday", true},
		{"calendar.work", "standup", false},
	}
	for i, w := range want {
		e := got[i]
		if e.Calendar != w.calendar || e.Summary != w.summary || e.AllDay != w.allDay {
			t.Errorf("event %d = %s %q all-day=%v, want %s %q all-day=%v",
				i, e.Calendar, e.Summary, e.AllDay, w.calendar, w.summary, w.allDay)
		}
	}
	if !got[0].End.Equal(got[0].Start) || got[0].Description != "notes" {
		t.Errorf("an event without an end should end at its start and keep its notes: %+v", got[0])
	}
}

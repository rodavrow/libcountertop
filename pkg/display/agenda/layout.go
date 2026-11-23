package agenda

import (
	"slices"
	"time"

	"github.com/ygelfand/libcountertop/pkg/homeassistant/calendar"
)

func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func addDays(t time.Time, n int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day()+n, 0, 0, 0, 0, t.Location())
}

func daysBetween(from, to time.Time) int {
	a := time.Date(from.Year(), from.Month(), from.Day(), 12, 0, 0, 0, time.UTC)
	b := time.Date(to.Year(), to.Month(), to.Day(), 12, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}

func weekOf(t time.Time, first time.Weekday) time.Time {
	d := day(t)
	return addDays(d, -((int(d.Weekday()) - int(first) + 7) % 7))
}

func monthGrid(anchor time.Time, first time.Weekday) (time.Time, int) {
	start := time.Date(anchor.Year(), anchor.Month(), 1, 0, 0, 0, 0, anchor.Location())
	from := weekOf(start, first)
	end := start.AddDate(0, 1, 0)
	weeks := (daysBetween(from, end) + 6) / 7
	return from, weeks
}

func lastDay(e calendar.Event) time.Time {
	end := e.End
	if !end.After(e.Start) {
		return day(e.Start)
	}
	return day(end.Add(-time.Nanosecond))
}

func spans(e calendar.Event) bool {
	return e.AllDay || !day(e.Start).Equal(lastDay(e))
}

type bar struct {
	event          calendar.Event
	from, to, lane int
	before, after  bool
}

func bars(events []calendar.Event, start time.Time, days int) []bar {
	end := addDays(start, days)
	var out []bar
	for _, e := range events {
		if !spans(e) || !e.Overlaps(start, end) {
			continue
		}
		from := daysBetween(start, day(e.Start))
		to := daysBetween(start, lastDay(e))
		out = append(out, bar{
			event:  e,
			from:   max(from, 0),
			to:     min(to, days-1),
			before: from < 0,
			after:  to > days-1,
		})
	}
	slices.SortStableFunc(out, func(a, b bar) int {
		if a.from != b.from {
			return a.from - b.from
		}
		return (b.to - b.from) - (a.to - a.from)
	})
	var ends []int
	for i := range out {
		lane := slices.IndexFunc(ends, func(last int) bool { return last < out[i].from })
		if lane < 0 {
			lane = len(ends)
			ends = append(ends, 0)
		}
		ends[lane] = out[i].to
		out[i].lane = lane
	}
	return out
}

func lanes(placed []bar, at int) int {
	n := 0
	for _, b := range placed {
		if b.from <= at && b.to >= at {
			n = max(n, b.lane+1)
		}
	}
	return n
}

func timed(events []calendar.Event, d time.Time) []calendar.Event {
	var out []calendar.Event
	for _, e := range events {
		if !spans(e) && day(e.Start).Equal(d) {
			out = append(out, e)
		}
	}
	return out
}

type block struct {
	event     calendar.Event
	col, cols int
}

func columns(events []calendar.Event) []block {
	sorted := slices.Clone(events)
	slices.SortStableFunc(sorted, func(a, b calendar.Event) int { return a.Start.Compare(b.Start) })
	var (
		out     []block
		cluster []int
		ends    []time.Time
		reach   time.Time
	)
	finish := func() {
		for _, i := range cluster {
			out[i].cols = len(ends)
		}
		cluster, ends = nil, nil
	}
	for _, e := range sorted {
		end := e.End
		if !end.After(e.Start) {
			end = e.Start.Add(30 * time.Minute)
		}
		if len(cluster) > 0 && !e.Start.Before(reach) {
			finish()
		}
		col := slices.IndexFunc(ends, func(t time.Time) bool { return !t.After(e.Start) })
		if col < 0 {
			col = len(ends)
			ends = append(ends, end)
		} else {
			ends[col] = end
		}
		if end.After(reach) || len(cluster) == 0 {
			reach = end
		}
		cluster = append(cluster, len(out))
		out = append(out, block{event: e, col: col})
	}
	finish()
	return out
}

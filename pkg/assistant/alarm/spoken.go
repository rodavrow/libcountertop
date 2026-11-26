package alarm

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/say"
	"github.com/ygelfand/libcountertop/pkg/voice"
)

var errSpokenTime = errors.New("alarm: no time in what was said")

func spokenClock(text string) (int, int, error) {
	for _, l := range []voice.Language{voice.Current(), voice.English()} {
		if h, m, ok := l.Clock(text); ok {
			return h, m, nil
		}
	}
	return 0, 0, errSpokenTime
}

func spokenDays(text string) (Weekdays, bool) {
	for _, l := range []voice.Language{voice.Current(), voice.English()} {
		if d, ok := l.Days(text); ok {
			return d, true
		}
	}
	if d, err := ParseWeekdays(text); err == nil && len(d) > 0 {
		return d, true
	}
	return nil, false
}

func spokenSchedule(text, days string) (int, int, Weekdays, error) {
	if strings.TrimSpace(days) != "" {
		repeat, ok := spokenDays(days)
		if !ok {
			return 0, 0, nil, errSpokenTime
		}
		h, m, err := spokenClock(text)
		return h, m, sorted(repeat), err
	}
	if h, m, err := spokenClock(text); err == nil {
		return h, m, nil, nil
	}
	words := strings.Fields(text)
	for i := 1; i < len(words); i++ {
		h, m, err := spokenClock(strings.Join(words[:i], " "))
		if err != nil {
			continue
		}
		if repeat, ok := spokenDays(strings.Join(words[i:], " ")); ok {
			return h, m, sorted(repeat), nil
		}
	}
	return 0, 0, nil, errSpokenTime
}

func sorted(w Weekdays) Weekdays {
	w = slices.Clone(w)
	slices.Sort(w)
	return slices.Compact(w)
}

func spokenNext(target string) bool {
	target = voice.TrimArticles(voice.Current(), strings.ToLower(strings.TrimSpace(target)))
	for _, l := range []voice.Language{voice.Current(), voice.English()} {
		if slices.Contains(l.Next(), target) {
			return true
		}
	}
	return false
}

func spokenDayNames(w Weekdays) string {
	switch {
	case slices.Equal(w, Weekdays{0, 1, 2, 3, 4, 5, 6}):
		return say.Speak("actions.alarm.daily")
	case slices.Equal(w, Weekdays{1, 2, 3, 4, 5}):
		return say.Speak("actions.alarm.weekdays")
	case slices.Equal(w, Weekdays{0, 6}):
		return say.Speak("actions.alarm.weekends")
	}
	names := make([]string, 0, len(w))
	for _, d := range w {
		names = append(names, spokenDay(d))
	}
	return strings.Join(names, ", ")
}

func spokenDay(d time.Weekday) string { return say.Speak("alarm.day." + dayNames[d]) }

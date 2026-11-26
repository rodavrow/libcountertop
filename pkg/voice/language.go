package voice

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/say"
)

type Language interface {
	Numbers(words []string) []string
	Clock(text string) (hour, minute int, ok bool)
	Days(text string) ([]time.Weekday, bool)
	Articles() []string
	Connectors() []string
	Next() []string
}

var languages = map[string]Language{
	"de": german{},
	"en": english{},
	"it": italian{},
	"nl": dutch{},
	"vi": vietnamese{},
}

func Current() Language { return For(say.Voice()) }

func For(tag string) Language {
	if l, ok := languages[tag]; ok {
		return l
	}
	return english{}
}

func English() Language { return english{} }

func Tags() []string {
	out := make([]string, 0, len(languages))
	for tag := range languages {
		out = append(out, tag)
	}
	slices.Sort(out)
	return out
}

type period func(hour int) (int, bool)

func morning(h int) (int, bool) {
	if h > 12 {
		return 0, false
	}
	return h % 12, true
}

func afternoon(h int) (int, bool) {
	if h > 12 {
		return h, h < 24
	}
	if h == 12 {
		return 12, true
	}
	return h + 12, true
}

func night(h int) (int, bool) {
	switch {
	case h > 12:
		return h, h < 24
	case h == 12:
		return 0, true
	case h < 6:
		return h, true
	}
	return h + 12, true
}

func midday(h int) (int, bool) {
	switch {
	case h > 12:
		return h, h < 24
	case h >= 10:
		return h, true
	}
	return h + 12, true
}

type spoken struct {
	hour, minute, adjust int
	set                  bool
	at                   period
}

func (s spoken) resolve() (int, int, bool) {
	if !s.set || s.hour < 0 || s.hour > 23 || s.minute < 0 || s.minute > 59 {
		return 0, 0, false
	}
	if s.adjust != 0 && s.minute != 0 {
		return 0, 0, false
	}
	hour := s.hour
	if s.at != nil {
		var ok bool
		if hour, ok = s.at(hour); !ok {
			return 0, 0, false
		}
	}
	minutes := (hour*60 + s.minute + s.adjust + 24*60) % (24 * 60)
	return minutes / 60, minutes % 60, true
}

func digits(word string) (int, bool) {
	n, err := strconv.Atoi(word)
	return n, err == nil && n >= 0
}

func digitClock(word string) (int, int, bool) {
	for _, sep := range []string{":", ".", "h"} {
		if left, right, ok := strings.Cut(word, sep); ok {
			h, okh := digits(left)
			if right == "" {
				return h, 0, okh && sep == "h"
			}
			m, okm := digits(right)
			return h, m, okh && okm && len(right) == 2
		}
	}
	h, ok := digits(word)
	return h, 0, ok
}

func fields(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.NewReplacer(",", " ", "!", " ", "?", " ").Replace(text)
	return strings.Fields(text)
}

func dropLeading(words []string, fillers ...string) []string {
	for len(words) > 0 && slices.Contains(fillers, words[0]) {
		words = words[1:]
	}
	return words
}

func hasPrefix(words, prefix []string) bool {
	return len(words) >= len(prefix) && slices.Equal(words[:len(prefix)], prefix)
}

func trailingPeriod(words []string, periods map[string]period) ([]string, period) {
	for n := min(len(words), 3); n > 0; n-- {
		if at, ok := periods[strings.Join(words[len(words)-n:], " ")]; ok {
			return words[:len(words)-n], at
		}
	}
	return words, nil
}

func dayList(words []string, names map[string]time.Weekday, joins []string) ([]time.Weekday, bool) {
	var out []time.Weekday
	for i := 0; i < len(words); {
		if slices.Contains(joins, words[i]) {
			i++
			continue
		}
		matched := false
		for n := min(len(words)-i, 2); n > 0; n-- {
			if d, ok := names[strings.Join(words[i:i+n], " ")]; ok {
				if !slices.Contains(out, d) {
					out = append(out, d)
				}
				i += n
				matched = true
				break
			}
		}
		if !matched {
			return nil, false
		}
	}
	slices.Sort(out)
	return out, len(out) > 0
}

func dayRange(from, to time.Weekday) []time.Weekday {
	var out []time.Weekday
	for d := from; ; d = (d + 1) % 7 {
		out = append(out, d)
		if d == to {
			break
		}
	}
	slices.Sort(out)
	return out
}

var (
	everyDay = []time.Weekday{0, 1, 2, 3, 4, 5, 6}
	workDays = []time.Weekday{1, 2, 3, 4, 5}
	weekend  = []time.Weekday{0, 6}
)

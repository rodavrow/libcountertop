package voice

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type english struct{}

var hyphenated = regexp.MustCompile(`(\pL)-(\pL)`)

var englishUnits = map[string]int{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
	"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
	"fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
	"nineteen": 19,
}

var englishTens = map[string]int{
	"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
	"sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
}

var englishPeriods = map[string]period{
	"am":               am,
	"pm":               pm,
	"in the morning":   morning,
	"in the afternoon": afternoon,
	"in the evening":   afternoon,
	"at night":         night,
}

var englishDays = map[string]time.Weekday{}

func init() {
	for d := time.Sunday; d <= time.Saturday; d++ {
		full := strings.ToLower(d.String())
		englishDays[full] = d
		englishDays[full+"s"] = d
		englishDays[full[:3]] = d
	}
	englishDays["tues"] = time.Tuesday
	englishDays["thurs"] = time.Thursday
}

func am(h int) (int, bool) {
	if h < 1 || h > 12 {
		return 0, false
	}
	return h % 12, true
}

func pm(h int) (int, bool) {
	if h < 1 || h > 12 {
		return 0, false
	}
	return h%12 + 12, true
}

func (english) Numbers(words []string) []string {
	out := make([]string, 0, len(words))
	for i := 0; i < len(words); {
		n, used := englishNumber(words[i:])
		if used == 0 {
			out = append(out, words[i])
			i++
			continue
		}
		out = append(out, strconv.Itoa(n))
		i += used
	}
	return out
}

func englishNumber(words []string) (value, used int) {
	i := 0
	if len(words) > 1 && words[0] == "a" && words[1] == "hundred" {
		value, i = 1, 1
	} else if v, ok := englishTens[words[0]]; ok {
		value, i = v, 1
		if len(words) > 1 {
			if u, ok := englishUnits[words[1]]; ok && u > 0 && u < 10 {
				value, i = v+u, 2
			}
		}
	} else if u, ok := englishUnits[words[0]]; ok {
		value, i = u, 1
	} else {
		return 0, 0
	}
	if i < len(words) && words[i] == "hundred" && value > 0 && value < 10 {
		value *= 100
		i++
		j := i
		if j < len(words) && words[j] == "and" {
			j++
		}
		if j < len(words) {
			if rest, n := englishNumber(words[j:]); n > 0 && rest < 100 {
				value += rest
				i = j + n
			}
		}
	}
	return value, i
}

func (e english) Clock(text string) (int, int, bool) {
	text = strings.NewReplacer(
		"a.m.", " am", "p.m.", " pm", "a.m", " am", "p.m", " pm", "o'clock", " ",
	).Replace(strings.ToLower(text))
	text = hyphenated.ReplaceAllString(text, "$1 $2")
	w := dropLeading(fields(text), "at", "for")
	switch strings.Join(w, " ") {
	case "midnight":
		return 0, 0, true
	case "noon", "midday":
		return 12, 0, true
	}
	if n := len(w); n > 0 {
		for _, suffix := range []string{"am", "pm"} {
			if head, ok := strings.CutSuffix(w[n-1], suffix); ok && head != "" {
				w = append(w[:n-1:n-1], head, suffix)
				break
			}
		}
	}
	var s spoken
	w, s.at = trailingPeriod(w, englishPeriods)
	for _, p := range []struct {
		words  []string
		adjust int
	}{
		{[]string{"a", "quarter", "past"}, 15},
		{[]string{"quarter", "past"}, 15},
		{[]string{"half", "past"}, 30},
		{[]string{"a", "quarter", "to"}, -15},
		{[]string{"quarter", "to"}, -15},
	} {
		if hasPrefix(w, p.words) {
			s.adjust, w = p.adjust, w[len(p.words):]
			break
		}
	}
	for i, word := range w {
		if word == "oh" {
			w[i] = "0"
		}
	}
	w = slices.DeleteFunc(
		e.Numbers(w),
		func(x string) bool { return x == "minutes" || x == "minute" },
	)
	if len(w) >= 3 && s.adjust == 0 {
		if n, ok := digits(w[0]); ok {
			switch w[1] {
			case "past", "after":
				s.adjust, w = n, w[2:]
			case "to", "before":
				s.adjust, w = -n, w[2:]
			}
		}
	}
	s.hour, s.minute, s.set = clockTokens(w)
	return s.resolve()
}

func clockTokens(w []string) (int, int, bool) {
	switch len(w) {
	case 1:
		return digitClock(w[0])
	case 2:
		h, okh := digits(w[0])
		m, okm := digits(w[1])
		return h, m, okh && okm
	case 3:
		if w[1] == "0" {
			h, okh := digits(w[0])
			m, okm := digits(w[2])
			return h, m, okh && okm && m < 10
		}
	}
	return 0, 0, false
}

func (english) Days(text string) ([]time.Weekday, bool) {
	w := dropLeading(fields(text), "every", "each", "on", "the")
	switch strings.Join(w, " ") {
	case "day", "days", "daily":
		return everyDay, true
	case "weekday", "weekdays", "workday", "workdays", "week day", "week days":
		return workDays, true
	case "weekend", "weekends":
		return weekend, true
	}
	if len(w) == 3 && slices.Contains([]string{"to", "through", "thru", "until", "till"}, w[1]) {
		from, okf := englishDays[w[0]]
		to, okt := englishDays[w[2]]
		if okf && okt {
			return dayRange(from, to), true
		}
	}
	return dayList(w, englishDays, []string{"and", "&"})
}

func (english) Articles() []string { return []string{"the"} }

func (english) Connectors() []string { return []string{"on"} }

func (english) Next() []string { return []string{"next"} }

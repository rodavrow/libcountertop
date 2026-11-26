package voice

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

type german struct{}

var germanUnits = map[string]int{
	"null": 0, "eins": 1, "zwei": 2, "zwo": 2, "drei": 3, "vier": 4, "fünf": 5, "sechs": 6,
	"sieben": 7, "acht": 8, "neun": 9, "zehn": 10, "elf": 11, "zwölf": 12, "dreizehn": 13,
	"vierzehn": 14, "fünfzehn": 15, "sechzehn": 16, "siebzehn": 17, "achtzehn": 18,
	"neunzehn": 19,
}

var germanTens = map[string]int{
	"zwanzig": 20, "dreissig": 30, "vierzig": 40, "fünfzig": 50, "sechzig": 60,
	"siebzig": 70, "achtzig": 80, "neunzig": 90,
}

var germanCompound = map[string]int{
	"ein": 1, "zwei": 2, "drei": 3, "vier": 4, "fünf": 5, "sechs": 6, "sieben": 7,
	"acht": 8, "neun": 9,
}

var germanPeriods = map[string]period{
	"morgens":       morning,
	"früh":          morning,
	"in der früh":   morning,
	"am morgen":     morning,
	"vormittags":    morning,
	"am vormittag":  morning,
	"mittags":       midday,
	"am mittag":     midday,
	"nachmittags":   afternoon,
	"am nachmittag": afternoon,
	"abends":        afternoon,
	"am abend":      afternoon,
	"nachts":        night,
	"in der nacht":  night,
}

var germanDays = map[string]time.Weekday{
	"sonntag": time.Sunday, "sonntags": time.Sunday,
	"montag": time.Monday, "montags": time.Monday,
	"dienstag": time.Tuesday, "dienstags": time.Tuesday,
	"mittwoch": time.Wednesday, "mittwochs": time.Wednesday,
	"donnerstag": time.Thursday, "donnerstags": time.Thursday,
	"freitag": time.Friday, "freitags": time.Friday,
	"samstag": time.Saturday, "samstags": time.Saturday, "sonnabend": time.Saturday,
}

func (german) Numbers(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if n, ok := germanNumber(w); ok {
			out = append(out, strconv.Itoa(n))
			continue
		}
		out = append(out, w)
	}
	return out
}

func germanNumber(w string) (int, bool) {
	w = strings.ReplaceAll(w, "ß", "ss")
	if n, ok := germanUnits[w]; ok {
		return n, true
	}
	if n, ok := germanTens[w]; ok {
		return n, true
	}
	if w == "hundert" {
		return 100, true
	}
	if unit, tens, ok := strings.Cut(w, "und"); ok {
		u, oku := germanCompound[unit]
		t, okt := germanTens[tens]
		return u + t, oku && okt
	}
	return 0, false
}

func (g german) Clock(text string) (int, int, bool) {
	w := dropLeading(fields(text), "um", "für", "auf", "gegen", "ab")
	var s spoken
	w, s.at = trailingPeriod(w, germanPeriods)
	for i, word := range w {
		switch word {
		case "mittag":
			w[i] = "12"
		case "mitternacht":
			w[i] = "0"
		case "ein", "eine":
			w[i] = "1"
		}
	}
	w = slices.DeleteFunc(g.Numbers(w), func(x string) bool {
		return x == "uhr" || x == "minuten" || x == "minute"
	})
	switch {
	case len(w) == 2 && (w[0] == "halb" || w[0] == "dreiviertel"):
		h, ok := digits(w[1])
		if !ok {
			return 0, 0, false
		}
		s.hour, s.set = h, true
		s.adjust = map[string]int{"halb": -30, "dreiviertel": -15}[w[0]]
		return s.resolve()
	case len(w) == 3 && (w[1] == "nach" || w[1] == "vor"):
		m, okm := germanMinutes(w[0])
		h, okh := digits(w[2])
		if !okm || !okh {
			return 0, 0, false
		}
		s.hour, s.set = h, true
		if w[1] == "vor" {
			s.adjust = -m
		} else {
			s.minute = m
		}
		return s.resolve()
	case len(w) == 4 && (w[1] == "nach" || w[1] == "vor") && w[2] == "halb":
		m, okm := germanMinutes(w[0])
		h, okh := digits(w[3])
		if !okm || !okh || m >= 30 {
			return 0, 0, false
		}
		s.hour, s.set = h, true
		s.adjust = -30 + m
		if w[1] == "vor" {
			s.adjust = -30 - m
		}
		return s.resolve()
	}
	s.hour, s.minute, s.set = clockTokens(w)
	return s.resolve()
}

func germanMinutes(word string) (int, bool) {
	if word == "viertel" {
		return 15, true
	}
	m, ok := digits(word)
	return m, ok && m > 0 && m < 60
}

func (german) Days(text string) ([]time.Weekday, bool) {
	w := dropLeading(
		fields(text),
		"jeden", "jede", "jedes", "immer", "alle", "am", "an", "den", "die", "von", "vom",
	)
	switch strings.Join(w, " ") {
	case "tag", "tage", "täglich":
		return everyDay, true
	case "werktags", "werktag", "werktagen", "wochentags", "wochentag", "wochentagen",
		"unter der woche":
		return workDays, true
	case "wochenende", "wochenenden":
		return weekend, true
	}
	if len(w) == 3 && w[1] == "bis" {
		from, okf := germanDays[w[0]]
		to, okt := germanDays[w[2]]
		if okf && okt {
			return dayRange(from, to), true
		}
	}
	return dayList(w, germanDays, []string{"und", "sowie"})
}

func (german) Articles() []string { return []string{"der", "die", "das", "den", "dem"} }

func (german) Connectors() []string { return []string{"auf", "im"} }

func (german) Next() []string { return []string{"nächster", "nächste", "nächsten", "nächstes"} }

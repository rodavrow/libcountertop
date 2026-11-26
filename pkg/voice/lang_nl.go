package voice

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

type dutch struct{}

var dutchUnits = map[string]int{
	"nul": 0, "één": 1, "eén": 1, "twee": 2, "drie": 3, "vier": 4, "vijf": 5, "zes": 6,
	"zeven": 7, "acht": 8, "negen": 9, "tien": 10, "elf": 11, "twaalf": 12, "dertien": 13,
	"veertien": 14, "vijftien": 15, "zestien": 16, "zeventien": 17, "achttien": 18,
	"negentien": 19,
}

var dutchTens = map[string]int{
	"twintig": 20, "dertig": 30, "veertig": 40, "vijftig": 50, "zestig": 60,
	"zeventig": 70, "tachtig": 80, "negentig": 90,
}

var dutchCompound = []struct {
	word  string
	value int
}{
	{"een", 1}, {"twee", 2}, {"drie", 3}, {"vier", 4}, {"vijf", 5}, {"zes", 6},
	{"zeven", 7}, {"acht", 8}, {"negen", 9},
}

var dutchPeriods = map[string]period{
	"'s ochtends":   morning,
	"s ochtends":    morning,
	"'s morgens":    morning,
	"s morgens":     morning,
	"in de ochtend": morning,
	"'s middags":    afternoon,
	"s middags":     afternoon,
	"in de middag":  afternoon,
	"'s avonds":     afternoon,
	"s avonds":      afternoon,
	"in de avond":   afternoon,
	"'s nachts":     night,
	"s nachts":      night,
	"in de nacht":   night,
}

var dutchDays = map[string]time.Weekday{
	"zondag": time.Sunday, "zondags": time.Sunday,
	"maandag": time.Monday, "maandags": time.Monday,
	"dinsdag": time.Tuesday, "dinsdags": time.Tuesday,
	"woensdag": time.Wednesday, "woensdags": time.Wednesday,
	"donderdag": time.Thursday, "donderdags": time.Thursday,
	"vrijdag": time.Friday, "vrijdags": time.Friday,
	"zaterdag": time.Saturday, "zaterdags": time.Saturday,
}

func (dutch) Numbers(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if n, ok := dutchNumber(w); ok {
			out = append(out, strconv.Itoa(n))
			continue
		}
		out = append(out, w)
	}
	return out
}

func dutchNumber(w string) (int, bool) {
	if n, ok := dutchUnits[w]; ok {
		return n, true
	}
	if n, ok := dutchTens[w]; ok {
		return n, true
	}
	if w == "honderd" {
		return 100, true
	}
	for _, u := range dutchCompound {
		rest, ok := strings.CutPrefix(w, u.word)
		if !ok {
			continue
		}
		for _, and := range []string{"en", "ën"} {
			if tens, ok := strings.CutPrefix(rest, and); ok {
				if t, ok := dutchTens[tens]; ok {
					return u.value + t, true
				}
			}
		}
	}
	return 0, false
}

func (d dutch) Clock(text string) (int, int, bool) {
	w := dropLeading(fields(text), "om", "rond", "tegen", "op")
	var s spoken
	w, s.at = trailingPeriod(w, dutchPeriods)
	for i, word := range w {
		switch word {
		case "middernacht":
			w[i] = "0"
		case "een":
			w[i] = "1"
		}
	}
	w = slices.DeleteFunc(d.Numbers(w), func(x string) bool {
		return x == "uur" || x == "minuten" || x == "minuut"
	})
	switch {
	case len(w) == 2 && w[0] == "half":
		h, ok := digits(w[1])
		if !ok {
			return 0, 0, false
		}
		s.hour, s.adjust, s.set = h, -30, true
		return s.resolve()
	case len(w) == 3 && (w[1] == "over" || w[1] == "voor"):
		m, okm := dutchMinutes(w[0])
		h, okh := digits(w[2])
		if !okm || !okh {
			return 0, 0, false
		}
		s.hour, s.set = h, true
		if w[1] == "voor" {
			s.adjust = -m
		} else {
			s.minute = m
		}
		return s.resolve()
	case len(w) == 4 && (w[1] == "over" || w[1] == "voor") && w[2] == "half":
		m, okm := dutchMinutes(w[0])
		h, okh := digits(w[3])
		if !okm || !okh || m >= 30 {
			return 0, 0, false
		}
		s.hour, s.set = h, true
		s.adjust = -30 + m
		if w[1] == "voor" {
			s.adjust = -30 - m
		}
		return s.resolve()
	}
	s.hour, s.minute, s.set = clockTokens(w)
	return s.resolve()
}

func dutchMinutes(word string) (int, bool) {
	if word == "kwart" {
		return 15, true
	}
	m, ok := digits(word)
	return m, ok && m > 0 && m < 60
}

func (dutch) Days(text string) ([]time.Weekday, bool) {
	text = strings.NewReplacer("tot en met", "t/m").Replace(strings.ToLower(text))
	w := dropLeading(
		fields(text),
		"elke",
		"iedere",
		"ieder",
		"alle",
		"op",
		"in",
		"het",
		"de",
		"van",
	)
	switch strings.Join(w, " ") {
	case "dag", "dagen", "dagelijks":
		return everyDay, true
	case "werkdag", "werkdagen", "weekdag", "weekdagen", "doordeweeks", "door de week":
		return workDays, true
	case "weekend", "weekenden", "weekeinde", "weekeinden":
		return weekend, true
	}
	if len(w) == 3 && (w[1] == "tot" || w[1] == "t/m") {
		from, okf := dutchDays[w[0]]
		to, okt := dutchDays[w[2]]
		if okf && okt {
			return dayRange(from, to), true
		}
	}
	return dayList(w, dutchDays, []string{"en"})
}

func (dutch) Articles() []string { return []string{"de", "het"} }

func (dutch) Connectors() []string { return []string{"op"} }

func (dutch) Next() []string { return []string{"volgende", "eerstvolgende"} }

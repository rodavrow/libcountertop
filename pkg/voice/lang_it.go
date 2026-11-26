package voice

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

type italian struct{}

var italianUnits = map[string]int{
	"zero": 0, "uno": 1, "un": 1, "una": 1, "due": 2, "tre": 3, "tré": 3, "quattro": 4,
	"cinque": 5, "sei": 6, "sette": 7, "otto": 8, "nove": 9, "dieci": 10, "undici": 11,
	"dodici": 12, "tredici": 13, "quattordici": 14, "quindici": 15, "sedici": 16,
	"diciassette": 17, "diciotto": 18, "diciannove": 19,
}

var italianTens = []struct {
	word  string
	value int
}{
	{"venti", 20}, {"trenta", 30}, {"quaranta", 40}, {"cinquanta", 50},
	{"sessanta", 60}, {"settanta", 70}, {"ottanta", 80}, {"novanta", 90},
}

var italianPeriods = map[string]period{
	"di mattina":     morning,
	"della mattina":  morning,
	"del mattino":    morning,
	"di pomeriggio":  afternoon,
	"del pomeriggio": afternoon,
	"di sera":        afternoon,
	"della sera":     afternoon,
	"di notte":       night,
	"della notte":    night,
}

var italianDays = map[string]time.Weekday{
	"domenica": time.Sunday, "domeniche": time.Sunday, "dom": time.Sunday,
	"lunedì": time.Monday, "lunedi": time.Monday, "lun": time.Monday,
	"martedì": time.Tuesday, "martedi": time.Tuesday, "mar": time.Tuesday,
	"mercoledì": time.Wednesday, "mercoledi": time.Wednesday, "mer": time.Wednesday,
	"giovedì": time.Thursday, "giovedi": time.Thursday, "gio": time.Thursday,
	"venerdì": time.Friday, "venerdi": time.Friday, "ven": time.Friday,
	"sabato": time.Saturday, "sabati": time.Saturday, "sab": time.Saturday,
}

func (italian) Numbers(words []string) []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		if n, ok := italianNumber(w); ok {
			out = append(out, strconv.Itoa(n))
			continue
		}
		out = append(out, w)
	}
	return out
}

func italianNumber(w string) (int, bool) {
	if n, ok := italianUnits[w]; ok {
		return n, true
	}
	if w == "cento" {
		return 100, true
	}
	for _, t := range italianTens {
		if w == t.word {
			return t.value, true
		}
		if rest, ok := strings.CutPrefix(w, t.word); ok {
			if u, ok := italianUnits[rest]; ok && u > 0 && u < 10 {
				return t.value + u, true
			}
		}
		if rest, ok := strings.CutPrefix(
			w,
			t.word[:len(t.word)-1],
		); ok &&
			(rest == "uno" || rest == "otto") {
			return t.value + italianUnits[rest], true
		}
	}
	return 0, false
}

func (it italian) Clock(text string) (int, int, bool) {
	text = strings.NewReplacer("all'", "alle ", "dell'", "delle ", "l'", "le ").
		Replace(strings.ToLower(text))
	w := dropLeading(fields(text), "per", "verso", "alle", "dalle", "delle", "della", "le", "ore")
	for i, word := range w {
		switch word {
		case "mezzogiorno":
			w[i] = "12"
		case "mezzanotte":
			w[i] = "0"
		}
	}
	var s spoken
	w, s.at = trailingPeriod(w, italianPeriods)
	joined := strings.NewReplacer("un quarto", "quarto", "tre quarti", "trequarti").
		Replace(strings.Join(w, " "))
	w = slices.DeleteFunc(
		it.Numbers(strings.Fields(joined)),
		func(x string) bool { return x == "minuti" || x == "minuto" },
	)
	if len(w) == 3 && (w[1] == "e" || w[1] == "meno") {
		h, okh := digits(w[0])
		var m int
		okm := true
		switch w[2] {
		case "mezza", "mezzo":
			m = 30
		case "quarto":
			m = 15
		case "trequarti":
			m = 45
		default:
			m, okm = digits(w[2])
		}
		if !okh || !okm {
			return 0, 0, false
		}
		s.hour, s.set = h, true
		if w[1] == "meno" {
			s.adjust = -m
		} else {
			s.minute = m
		}
		return s.resolve()
	}
	s.hour, s.minute, s.set = clockTokens(w)
	return s.resolve()
}

func (italian) Days(text string) ([]time.Weekday, bool) {
	w := dropLeading(
		fields(text),
		"ogni",
		"tutti",
		"tutte",
		"i",
		"gli",
		"il",
		"la",
		"le",
		"nei",
		"nel",
		"di",
		"del",
		"dal",
		"da",
	)
	switch strings.Join(w, " ") {
	case "giorno", "giorni", "giorno della settimana", "giorni della settimana":
		return everyDay, true
	case "giorno feriale",
		"giorni feriali",
		"feriali",
		"giorno lavorativo",
		"giorni lavorativi",
		"lavorativi":
		return workDays, true
	case "fine settimana", "finesettimana", "weekend", "week end":
		return weekend, true
	}
	if len(w) == 3 && (w[1] == "al" || w[1] == "a") {
		from, okf := italianDays[w[0]]
		to, okt := italianDays[w[2]]
		if okf && okt {
			return dayRange(from, to), true
		}
	}
	return dayList(w, italianDays, []string{"e", "ed", "il", "la"})
}

func (italian) Articles() []string { return []string{"il", "lo", "la", "i", "gli", "le", "l'"} }

func (italian) Connectors() []string {
	return []string{"su", "sul", "sulla", "sullo", "sui", "sugli", "sulle", "nella", "nel"}
}

func (italian) Next() []string { return []string{"prossima", "prossimo"} }

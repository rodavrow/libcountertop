package voice

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/say"
)

func TestEveryCatalogLanguageHasAVoiceLanguage(t *testing.T) {
	if got, want := Tags(), say.Languages(); !slices.Equal(
		got,
		slices.Sorted(slices.Values(want)),
	) {
		t.Errorf("voice languages %v, catalog languages %v", got, want)
	}
}

type clockCase struct {
	said         string
	hour, minute int
}

func checkClocks(t *testing.T, l Language, cases []clockCase, refused []string) {
	t.Helper()
	for _, c := range cases {
		h, m, ok := l.Clock(c.said)
		if !ok || h != c.hour || m != c.minute {
			t.Errorf(
				"%q read as %02d:%02d (%v), want %02d:%02d",
				c.said,
				h,
				m,
				ok,
				c.hour,
				c.minute,
			)
		}
	}
	for _, said := range refused {
		if h, m, ok := l.Clock(said); ok {
			t.Errorf("%q read as %02d:%02d, want it refused", said, h, m)
		}
	}
}

type daysCase struct {
	said string
	want []time.Weekday
}

func checkDays(t *testing.T, l Language, cases []daysCase, refused []string) {
	t.Helper()
	for _, c := range cases {
		got, ok := l.Days(c.said)
		if !ok || !slices.Equal(got, c.want) {
			t.Errorf("%q read as %v (%v), want %v", c.said, got, ok, c.want)
		}
	}
	for _, said := range refused {
		if got, ok := l.Days(said); ok {
			t.Errorf("%q read as %v, want it refused", said, got)
		}
	}
}

func TestEnglishClock(t *testing.T) {
	checkClocks(t, English(), []clockCase{
		{"7", 7, 0},
		{"7:30", 7, 30},
		{"7:30 pm", 19, 30},
		{"7pm", 19, 0},
		{"seven thirty am", 7, 30},
		{"seven oh five", 7, 5},
		{"twenty two fifteen", 22, 15},
		{"quarter past seven", 7, 15},
		{"half past six pm", 18, 30},
		{"quarter to eight", 7, 45},
		{"ten past seven", 7, 10},
		{"twenty to nine in the evening", 20, 40},
		{"noon", 12, 0},
		{"midnight", 0, 0},
		{"12 am", 0, 0},
		{"seven o'clock", 7, 0},
	}, []string{"", "banana", "25:00", "13 pm", "7:75"})
}

func TestEnglishDays(t *testing.T) {
	checkDays(t, English(), []daysCase{
		{"every day", everyDay},
		{"weekdays", workDays},
		{"every weekend", weekend},
		{"monday", []time.Weekday{time.Monday}},
		{"on mondays and fridays", []time.Weekday{time.Monday, time.Friday}},
		{"monday to friday", workDays},
		{"sat, sun", weekend},
	}, []string{"", "someday", "monday and soon"})
}

func TestItalianClock(t *testing.T) {
	checkClocks(t, For("it"), []clockCase{
		{"7", 7, 0},
		{"alle 7:30", 7, 30},
		{"le sette", 7, 0},
		{"sette e mezza", 7, 30},
		{"sette e un quarto", 7, 15},
		{"otto meno un quarto", 7, 45},
		{"otto meno dieci", 7, 50},
		{"sette e venti", 7, 20},
		{"sette e tre quarti", 7, 45},
		{"diciannove e trenta", 19, 30},
		{"ventitré", 23, 0},
		{"sette di sera", 19, 0},
		{"sette e mezza di mattina", 7, 30},
		{"all'una", 1, 0},
		{"mezzogiorno", 12, 0},
		{"mezzanotte", 0, 0},
		{"mezzogiorno e mezza", 12, 30},
	}, []string{"", "banana", "venticinque", "sette e mezza e mezza"})
}

func TestItalianDays(t *testing.T) {
	checkDays(t, For("it"), []daysCase{
		{"ogni giorno", everyDay},
		{"tutti i giorni", everyDay},
		{"nei giorni feriali", workDays},
		{"nel fine settimana", weekend},
		{"il lunedì", []time.Weekday{time.Monday}},
		{"lunedì e venerdì", []time.Weekday{time.Monday, time.Friday}},
		{"dal lunedì al venerdì", workDays},
	}, []string{"", "domani"})
}

func TestVietnameseClock(t *testing.T) {
	checkClocks(t, For("vi"), []clockCase{
		{"7", 7, 0},
		{"7h30", 7, 30},
		{"7 giờ", 7, 0},
		{"bảy giờ", 7, 0},
		{"bảy giờ ba mươi", 7, 30},
		{"bảy giờ ba mươi phút", 7, 30},
		{"bảy giờ rưỡi", 7, 30},
		{"tám giờ kém mười lăm", 7, 45},
		{"bảy giờ mười lăm", 7, 15},
		{"lúc sáu giờ sáng", 6, 0},
		{"bảy giờ tối", 19, 0},
		{"hai giờ chiều", 14, 0},
		{"mười một giờ đêm", 23, 0},
		{"một giờ trưa", 13, 0},
		{"nửa đêm", 0, 0},
		{"hai mươi mốt giờ", 21, 0},
	}, []string{"", "chuối", "bảy giờ rưỡi ba mươi"})
}

func TestVietnameseDays(t *testing.T) {
	checkDays(t, For("vi"), []daysCase{
		{"mỗi ngày", everyDay},
		{"hằng ngày", everyDay},
		{"ngày thường", workDays},
		{"cuối tuần", weekend},
		{"thứ hai", []time.Weekday{time.Monday}},
		{"thứ hai và thứ sáu", []time.Weekday{time.Monday, time.Friday}},
		{"từ thứ hai đến thứ sáu", workDays},
		{"vào chủ nhật", []time.Weekday{time.Sunday}},
	}, []string{"", "ngày mai"})
}

func TestGermanClock(t *testing.T) {
	checkClocks(t, For("de"), []clockCase{
		{"7", 7, 0},
		{"um 7:30", 7, 30},
		{"sieben uhr", 7, 0},
		{"sieben uhr dreißig", 7, 30},
		{"19 uhr 30", 19, 30},
		{"halb acht", 7, 30},
		{"viertel nach sieben", 7, 15},
		{"viertel vor acht", 7, 45},
		{"dreiviertel acht", 7, 45},
		{"zehn nach sieben", 7, 10},
		{"zehn vor acht", 7, 50},
		{"fünf vor halb acht", 7, 25},
		{"fünf nach halb acht", 7, 35},
		{"einundzwanzig uhr", 21, 0},
		{"sieben uhr abends", 19, 0},
		{"halb acht abends", 19, 30},
		{"sechs uhr morgens", 6, 0},
		{"ein uhr", 1, 0},
		{"mittag", 12, 0},
		{"mitternacht", 0, 0},
	}, []string{"", "banane", "halb halb", "vierzig nach halb acht", "morgen"})
}

func TestGermanDays(t *testing.T) {
	checkDays(t, For("de"), []daysCase{
		{"jeden tag", everyDay},
		{"täglich", everyDay},
		{"werktags", workDays},
		{"an werktagen", workDays},
		{"unter der woche", workDays},
		{"am wochenende", weekend},
		{"montags", []time.Weekday{time.Monday}},
		{"montag und freitag", []time.Weekday{time.Monday, time.Friday}},
		{"von montag bis freitag", workDays},
	}, []string{"", "morgen"})
}

func TestDutchClock(t *testing.T) {
	checkClocks(t, For("nl"), []clockCase{
		{"7", 7, 0},
		{"om 7:30", 7, 30},
		{"zeven uur", 7, 0},
		{"zeven uur dertig", 7, 30},
		{"half acht", 7, 30},
		{"kwart over zeven", 7, 15},
		{"kwart voor acht", 7, 45},
		{"tien over zeven", 7, 10},
		{"tien voor acht", 7, 50},
		{"tien over half acht", 7, 40},
		{"vijf voor half acht", 7, 25},
		{"eenentwintig uur", 21, 0},
		{"tweeëntwintig uur", 22, 0},
		{"acht uur 's avonds", 20, 0},
		{"zes uur 's ochtends", 6, 0},
		{"twee uur 's middags", 14, 0},
		{"een uur", 1, 0},
		{"middernacht", 0, 0},
	}, []string{"", "banaan", "half half", "veertig over half acht", "morgen"})
}

func TestDutchDays(t *testing.T) {
	checkDays(t, For("nl"), []daysCase{
		{"elke dag", everyDay},
		{"dagelijks", everyDay},
		{"doordeweeks", workDays},
		{"op werkdagen", workDays},
		{"in het weekend", weekend},
		{"maandags", []time.Weekday{time.Monday}},
		{"maandag en vrijdag", []time.Weekday{time.Monday, time.Friday}},
		{"maandag tot en met vrijdag", workDays},
		{"van maandag t/m vrijdag", workDays},
	}, []string{"", "morgen"})
}

func TestNumbers(t *testing.T) {
	for _, c := range []struct {
		tag, said, want string
	}{
		{"en", "bedroom two", "bedroom 2"},
		{"en", "twenty three", "23"},
		{"it", "camera due", "camera 2"},
		{"it", "ventitré trentotto", "23 38"},
		{"vi", "phòng hai", "phòng 2"},
		{"vi", "hai mươi lăm", "25"},
		{"vi", "mười lăm", "15"},
		{"de", "schlafzimmer zwei", "schlafzimmer 2"},
		{"de", "einundzwanzig dreißig", "21 30"},
		{"de", "ein licht", "ein licht"},
		{"nl", "slaapkamer twee", "slaapkamer 2"},
		{"nl", "drieëntwintig vijfenveertig", "23 45"},
		{"nl", "een lamp", "een lamp"},
	} {
		if got := strings.Join(For(c.tag).Numbers(strings.Fields(c.said)), " "); got != c.want {
			t.Errorf("%s %q read as %q, want %q", c.tag, c.said, got, c.want)
		}
	}
}

func TestTrimArticles(t *testing.T) {
	for _, c := range []struct {
		tag, said, want string
	}{
		{"en", "the kitchen", "kitchen"},
		{"it", "la cucina", "cucina"},
		{"it", "l'ingresso", "ingresso"},
		{"vi", "phòng bếp", "phòng bếp"},
		{"de", "die küche", "küche"},
		{"nl", "de keuken", "keuken"},
	} {
		if got := TrimArticles(For(c.tag), c.said); got != c.want {
			t.Errorf("%s %q trimmed to %q, want %q", c.tag, c.said, got, c.want)
		}
	}
}

package voice

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

type vietnamese struct{}

var vietnameseUnits = map[string]int{
	"không": 0, "một": 1, "hai": 2, "ba": 3, "bốn": 4, "năm": 5,
	"sáu": 6, "bảy": 7, "bẩy": 7, "tám": 8, "chín": 9,
}

var vietnameseTail = map[string]int{
	"mốt": 1, "một": 1, "hai": 2, "ba": 3, "bốn": 4, "tư": 4, "lăm": 5, "nhăm": 5,
	"năm": 5, "sáu": 6, "bảy": 7, "bẩy": 7, "tám": 8, "chín": 9,
}

var vietnamesePeriods = map[string]period{
	"sáng":       morning,
	"buổi sáng":  morning,
	"trưa":       midday,
	"buổi trưa":  midday,
	"chiều":      afternoon,
	"buổi chiều": afternoon,
	"tối":        afternoon,
	"buổi tối":   afternoon,
	"đêm":        night,
	"ban đêm":    night,
}

var vietnameseDays = map[string]time.Weekday{
	"chủ nhật": time.Sunday, "cn": time.Sunday,
	"thứ hai": time.Monday, "t2": time.Monday,
	"thứ ba": time.Tuesday, "t3": time.Tuesday,
	"thứ tư": time.Wednesday, "t4": time.Wednesday,
	"thứ năm": time.Thursday, "t5": time.Thursday,
	"thứ sáu": time.Friday, "t6": time.Friday,
	"thứ bảy": time.Saturday, "thứ bẩy": time.Saturday, "t7": time.Saturday,
}

func (vietnamese) Numbers(words []string) []string {
	out := make([]string, 0, len(words))
	for i := 0; i < len(words); {
		n, used := vietnameseNumber(words[i:])
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

func vietnameseNumber(w []string) (int, int) {
	if w[0] == "mười" {
		if len(w) > 1 {
			if u, ok := vietnameseTail[w[1]]; ok && w[1] != "mốt" && w[1] != "tư" {
				return 10 + u, 2
			}
		}
		return 10, 1
	}
	u, ok := vietnameseUnits[w[0]]
	if !ok {
		return 0, 0
	}
	if len(w) > 1 && w[1] == "mươi" && u > 1 {
		if len(w) > 2 {
			if t, ok := vietnameseTail[w[2]]; ok {
				return u*10 + t, 3
			}
		}
		return u * 10, 2
	}
	if len(w) > 1 && w[1] == "trăm" && u > 0 {
		return u * 100, 2
	}
	return u, 1
}

func (vi vietnamese) Clock(text string) (int, int, bool) {
	w := dropLeading(fields(text), "vào", "lúc", "khoảng")
	switch strings.Join(w, " ") {
	case "nửa đêm":
		return 0, 0, true
	case "trưa", "giữa trưa", "buổi trưa":
		return 12, 0, true
	}
	var s spoken
	w, s.at = trailingPeriod(w, vietnamesePeriods)
	w = slices.DeleteFunc(vi.Numbers(w), func(x string) bool { return x == "phút" || x == "đúng" })
	if i := slices.Index(w, "kém"); i > 0 && i == len(w)-2 {
		m, ok := digits(w[i+1])
		if !ok {
			return 0, 0, false
		}
		s.adjust, w = -m, w[:i]
	}
	half := len(w) > 1 && w[len(w)-1] == "rưỡi"
	if half {
		w = w[:len(w)-1]
	}
	if len(w) >= 2 && w[1] == "giờ" {
		w = append(w[:1:1], w[2:]...)
	}
	s.hour, s.minute, s.set = clockTokens(w)
	if half {
		if s.minute != 0 {
			return 0, 0, false
		}
		s.minute = 30
	}
	return s.resolve()
}

func (vietnamese) Days(text string) ([]time.Weekday, bool) {
	w := dropLeading(fields(text), "vào", "các", "những", "từ")
	switch strings.Join(w, " ") {
	case "mỗi ngày", "hằng ngày", "hàng ngày", "mọi ngày", "ngày", "tất cả các ngày", "cả tuần":
		return everyDay, true
	case "ngày thường", "ngày trong tuần", "ngày làm việc", "mỗi ngày thường":
		return workDays, true
	case "cuối tuần", "mỗi cuối tuần", "ngày cuối tuần":
		return weekend, true
	}
	w = dropLeading(w, "mỗi", "hằng", "hàng")
	for _, to := range []string{"đến", "tới"} {
		if i := slices.Index(w, to); i > 0 {
			from, okf := vietnameseDays[strings.Join(w[:i], " ")]
			last, okt := vietnameseDays[strings.Join(w[i+1:], " ")]
			if okf && okt {
				return dayRange(from, last), true
			}
		}
	}
	return dayList(w, vietnameseDays, []string{"và"})
}

func (vietnamese) Articles() []string { return nil }

func (vietnamese) Connectors() []string { return []string{"trên"} }

func (vietnamese) Next() []string { return []string{"tiếp theo", "kế tiếp", "sắp tới"} }

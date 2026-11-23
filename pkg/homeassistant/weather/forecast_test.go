package weather

import (
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/homeassistant"
)

func on(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC) }

func TestDailyForecastKeepsEachDay(t *testing.T) {
	raw := []byte(`{"weather.home":{"forecast":[
		{"datetime":"2026-11-22T17:00:00+00:00","condition":"sunny","temperature":68.4,"templow":50},
		{"datetime":"2026-11-23T17:00:00+00:00","condition":"rainy","temperature":55},
		{"datetime":"not a time","condition":"cloudy","temperature":1}
	]}}`)
	days, err := parseForecast(raw, "weather.home", KindDaily, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 {
		t.Fatalf("got %d days, want 2", len(days))
	}
	first := days[0]
	if !first.Date.Equal(on(22, 0)) {
		t.Errorf("first day is %s", first.Date)
	}
	if first.Condition != "sunny" || first.High == nil || *first.High != 68.4 ||
		first.Low == nil || *first.Low != 50 {
		t.Errorf("first day = %+v", first)
	}
	if days[1].Low != nil {
		t.Errorf("second day has a low it was not given: %v", *days[1].Low)
	}
}

func TestTwiceDailyTakesTheDayHighAndTheNightLow(t *testing.T) {
	raw := []byte(`{"weather.home":{"forecast":[
		{"datetime":"2026-11-22T06:00:00+00:00","condition":"cloudy","temperature":64,"is_daytime":true},
		{"datetime":"2026-11-22T18:00:00+00:00","condition":"clear-night","temperature":48,"is_daytime":false},
		{"datetime":"2026-11-23T18:00:00+00:00","condition":"rainy","temperature":45,"is_daytime":false}
	]}}`)
	days, err := parseForecast(raw, "weather.home", KindTwice, time.UTC)
	if err != nil || len(days) != 2 {
		t.Fatalf("days = %+v, err = %v", days, err)
	}
	if d := days[0]; d.Condition != "cloudy" || *d.High != 64 || *d.Low != 48 {
		t.Errorf("22nd = %+v", d)
	}
	if d := days[1]; d.Condition != "rainy" || d.High != nil || *d.Low != 45 {
		t.Errorf("23rd, night only = %+v", d)
	}
}

func TestHourlyFoldsIntoDays(t *testing.T) {
	raw := []byte(`{"weather.home":{"forecast":[
		{"datetime":"2026-11-22T08:00:00+00:00","condition":"fog","temperature":51},
		{"datetime":"2026-11-22T13:00:00+00:00","condition":"sunny","temperature":66},
		{"datetime":"2026-11-22T22:00:00+00:00","condition":"clear-night","temperature":49}
	]}}`)
	days, err := parseForecast(raw, "weather.home", KindHourly, time.UTC)
	if err != nil || len(days) != 1 {
		t.Fatalf("days = %+v, err = %v", days, err)
	}
	if d := days[0]; d.Condition != "sunny" || *d.High != 66 || *d.Low != 49 {
		t.Errorf("22nd = %+v", d)
	}
}

func TestTheBestForecastWinsAndTheMainEntityBreaksTies(t *testing.T) {
	entity := func(id, features string) homeassistant.Entity {
		return homeassistant.Entity{ID: id, Attributes: map[string]string{attrFeatures: features}}
	}
	list := []homeassistant.Entity{
		entity("weather.hourly", "2"),
		entity("weather.daily", "3"),
		entity("weather.main", "1"),
		entity("weather.none", "0"),
	}
	if got := bestForecast(list, "weather.main"); got != "weather.main" {
		t.Errorf("tie went to %s, want the main entity", got)
	}
	if got := bestForecast(list[:2], "weather.hourly"); got != "weather.daily" {
		t.Errorf("best = %s, want the one with a daily forecast", got)
	}
	if got := bestForecast(list[3:], "weather.none"); got != "" {
		t.Errorf("best = %q with no forecasts at all", got)
	}
}

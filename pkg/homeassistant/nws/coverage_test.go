package nws

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/ygelfand/libcountertop/pkg/runtime/remember"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type refuseTransport struct{ t *testing.T }

func (r refuseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("asked the weather service about a home it does not cover: %s", req.URL)
	return nil, errors.New("no network in this test")
}

func TestPollAsksNothingForAHomeOutsideTheUS(t *testing.T) {
	store, err := remember.Open(filepath.Join(t.TempDir(), "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	n := New(Options{
		Client:    &http.Client{Transport: refuseTransport{t}},
		Settings:  &weatherSection{v: schema.Weather{Alerts: true}},
		Dismissed: remember.NewSlot[[]string](store, "weather.dismissed"),
	})
	n.home, n.located = [2]float64{52.15, 4.40}, true

	n.poll(t.Context())

	if n.problem != nil || !n.loaded {
		t.Errorf("after polling, problem = %v and loaded = %v; want none and loaded", n.problem, n.loaded)
	}
	if _, ok := n.Current(); ok {
		t.Error("an alert for a home the service does not cover")
	}
}

func TestCoveredKnowsWhereTheWeatherServiceIssuesAlerts(t *testing.T) {
	for _, tt := range []struct {
		name     string
		lat, lon float64
		want     bool
	}{
		{"Denver", 39.74, -104.99, true},
		{"Miami", 25.76, -80.19, true},
		{"Anchorage", 61.22, -149.90, true},
		{"Adak, west of the date line's US side", 51.88, -176.66, true},
		{"Attu, east of the date line", 52.93, 172.91, true},
		{"Honolulu", 21.31, -157.86, true},
		{"San Juan", 18.47, -66.11, true},
		{"Guam", 13.44, 144.79, true},
		{"Pago Pago", -14.28, -170.70, true},
		{"Leiden", 52.15, 4.40, false},
		{"London", 51.51, -0.13, false},
		{"Toronto", 43.65, -79.38, true}, // a box, not a border: alerts there are simply empty
		{"Mexico City", 19.43, -99.13, false},
		{"Sydney", -33.87, 151.21, false},
	} {
		if got := covered(tt.lat, tt.lon); got != tt.want {
			t.Errorf("covered(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

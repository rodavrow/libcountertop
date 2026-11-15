package cast

import (
	"errors"
	"net/http"
	"testing"
)

type refuseTransport struct{ t *testing.T }

func (r refuseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("a request went out with Google lookups off: %s", req.URL)
	return nil, errors.New("no network in this test")
}

func TestAnEnvWithoutACloudSwitchMayAskGoogle(t *testing.T) {
	if !(Env{}).CloudAllowed() {
		t.Error("an Env with no Cloud switch refuses Google lookups")
	}
}

func TestDescribeAsksNothingWithTheCloudOff(t *testing.T) {
	env := Env{
		HTTP:  &http.Client{Transport: refuseTransport{t}},
		Cloud: func() bool { return false },
	}
	if _, err := env.Describe(t.Context(), "CC1AD845"); !errors.Is(err, ErrCloudOff) {
		t.Errorf("Describe = %v, want ErrCloudOff", err)
	}
}

func TestDescribeUsesTheKnownConfigWithTheCloudOff(t *testing.T) {
	configs.Lock()
	configs.known["CACHED01"] = AppConfig{URL: "https://example.test/receiver", Name: "Kept"}
	configs.Unlock()
	t.Cleanup(func() {
		configs.Lock()
		delete(configs.known, "CACHED01")
		configs.Unlock()
	})
	env := Env{
		HTTP:  &http.Client{Transport: refuseTransport{t}},
		Cloud: func() bool { return false },
	}
	c, err := env.Describe(t.Context(), "CACHED01")
	if err != nil || c.Name != "Kept" {
		t.Errorf("Describe = %+v, %v; want the config already known", c, err)
	}
}

package videoplay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
)

// The public addresses here are in the documentation ranges, so nothing real is ever reached.
const (
	publicIP      = "203.0.113.7"
	publicURL     = "https://" + publicIP + "/a.mp4"
	publicSegment = "http://" + publicIP + "/live/seg_00042.ts"
)

func always(on bool) func() bool { return func() bool { return on } }

func TestLocalTakesOnlyPrivateLoopbackAndLinkLocal(t *testing.T) {
	cases := map[string]bool{
		"10.1.2.3":           true,
		"172.16.0.1":         true,
		"172.31.255.254":     true,
		"192.168.20.82":      true,
		"127.0.0.1":          true,
		"169.254.10.1":       true,
		"::1":                true,
		"fd12:3456::1":       true,
		"fe80::1":            true,
		"::ffff:192.168.1.1": true,
		"8.8.8.8":            false,
		"172.32.0.1":         false,
		"100.64.0.1":         false,
		"0.0.0.0":            false,
		"224.0.0.251":        false,
		"255.255.255.255":    false,
		"2001:4860::8888":    false,
		"::ffff:8.8.8.8":     false,
	}
	for addr, want := range cases {
		if got := local(netip.MustParseAddr(addr)); got != want {
			t.Errorf("local(%s) = %v, want %v", addr, got, want)
		}
	}
}

func localOnlyResolving(addrs ...string) *guard {
	g := newGuard(always(true))
	g.lookup = func(context.Context, string) ([]netip.Addr, error) {
		out := make([]netip.Addr, len(addrs))
		for i, a := range addrs {
			out[i] = netip.MustParseAddr(a)
		}
		return out, nil
	}
	return g
}

func TestWithoutLocalOnlyAPublicAddressIsAccepted(t *testing.T) {
	for name, g := range map[string]*guard{"nil": newGuard(nil), "false": newGuard(always(false))} {
		if _, err := g.parse(context.Background(), publicURL); err != nil {
			t.Errorf("LocalOnly %s: a public url was refused: %v", name, err)
		}
		if err := g.dial("tcp", publicIP+":443", nil); err != nil {
			t.Errorf("LocalOnly %s: the dialer refused a public address: %v", name, err)
		}
	}
}

func TestLocalOnlyAcceptsAPrivateAddress(t *testing.T) {
	g := newGuard(always(true))
	if _, err := g.parse(context.Background(), "http://192.168.1.10:8099/a.mp4"); err != nil {
		t.Fatalf("a private address was refused: %v", err)
	}
	if err := g.dial("tcp", "192.168.1.10:8099", nil); err != nil {
		t.Fatalf("the dialer refused a private address: %v", err)
	}
}

func TestLocalOnlyRefusesAPublicAddress(t *testing.T) {
	_, err := newGuard(always(true)).parse(context.Background(), publicURL)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("a public address gave %v, want ErrBlocked", err)
	}
}

func TestLocalOnlyRefusesANameThatResolvesPublicly(t *testing.T) {
	_, err := localOnlyResolving(publicIP).parse(context.Background(), "https://example.com/a.mp4")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("a public name gave %v, want ErrBlocked", err)
	}
}

func TestLocalOnlyRefusesANameWithAnyPublicAddress(t *testing.T) {
	_, err := localOnlyResolving("192.168.1.10", publicIP).
		parse(context.Background(), "http://nas.lan/a.mp4")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("a name with one public address gave %v, want ErrBlocked", err)
	}
}

func TestLocalOnlyAcceptsANameThatResolvesLocally(t *testing.T) {
	_, err := localOnlyResolving("192.168.1.10").parse(context.Background(), "http://nas.lan/a.mp4")
	if err != nil {
		t.Fatalf("a local name was refused: %v", err)
	}
}

func TestLocalOnlyRefusesANameThatDoesNotResolve(t *testing.T) {
	g := newGuard(always(true))
	g.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("nxdomain")
	}
	if _, err := g.parse(context.Background(), "http://nowhere.lan/a.mp4"); err == nil {
		t.Fatal("a name that does not resolve was accepted")
	}
}

func TestOnlyHTTPSchemesAreAccepted(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd", "rtsp://192.168.1.10/a", "ftp://192.168.1.10/a", "/a.mp4",
	} {
		if _, err := newGuard(nil).parse(context.Background(), raw); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}

func TestTheSettingIsReadAtEveryCheck(t *testing.T) {
	var on atomic.Bool
	g := newGuard(on.Load)
	if _, err := g.parse(context.Background(), publicURL); err != nil {
		t.Fatalf("a public url was refused before Local only: %v", err)
	}
	on.Store(true)
	if _, err := g.parse(context.Background(), publicURL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("a public url after Local only gave %v, want ErrBlocked", err)
	}
	if err := g.dial("tcp", publicIP+":80", nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("dialing out after Local only gave %v, want ErrBlocked", err)
	}
}

func redirecting(to string) string {
	return "/r?to=" + url.QueryEscape(to)
}

func redirector(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to := r.URL.Query().Get("to"); to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, c *http.Client, url string) error {
	t.Helper()
	resp, err := c.Get(url)
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}

func TestLocalOnlyRefusesARedirectToAPublicAddress(t *testing.T) {
	srv := redirector(t)
	g := newGuard(always(true))
	if err := get(t, g.client(nil), srv.URL+redirecting(publicURL)); !errors.Is(err, ErrBlocked) {
		t.Fatalf("a redirect to a public address gave %v, want ErrBlocked", err)
	}
	if g.refusals.Load() == 0 {
		t.Fatal("the refused redirect was not counted")
	}
}

func TestLocalOnlyFollowsARedirectWithinTheNetwork(t *testing.T) {
	srv := redirector(t)
	err := get(t, newGuard(always(true)).client(nil), srv.URL+redirecting(srv.URL+"/a"))
	if err != nil {
		t.Fatalf("a local redirect was refused: %v", err)
	}
}

func TestLocalOnlyRefusesAnHLSSegmentOutside(t *testing.T) {
	g := newGuard(always(true))
	if err := get(t, g.client(nil), publicSegment); !errors.Is(err, ErrBlocked) {
		t.Fatalf("fetching a public segment gave %v, want ErrBlocked", err)
	}
	if err := g.dial("tcp", publicIP+":80", nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("the dialer let the segment host through: %v", err)
	}
}

// With every address counted as outside, the test server stands in for a public one.
func outsideIsEverything(g *guard) *guard {
	g.local = func(netip.Addr) bool { return false }
	return g
}

func TestTheDefaultClientRefusesToConnectOutside(t *testing.T) {
	srv := redirector(t)
	g := outsideIsEverything(newGuard(always(true)))
	if err := get(t, g.client(nil), srv.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("connecting outside gave %v, want ErrBlocked", err)
	}
}

func TestAGivenClientChecksEveryRequest(t *testing.T) {
	srv := redirector(t)
	g := outsideIsEverything(newGuard(always(true)))
	if err := get(t, g.client(&http.Client{}), srv.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("a given client reached a refused address: %v", err)
	}
	outside := srv.URL + redirecting(publicURL)
	err := get(t, newGuard(always(true)).client(&http.Client{}), outside)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("a given client followed a redirect outside: %v", err)
	}
}

func TestAConnectionOpenedBeforeLocalOnlyIsNotReused(t *testing.T) {
	srv := redirector(t)
	var on atomic.Bool
	c := outsideIsEverything(newGuard(on.Load)).client(nil)
	if err := get(t, c, srv.URL); err != nil {
		t.Fatalf("the first request failed: %v", err)
	}
	on.Store(true)
	if err := get(t, c, srv.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("a kept-alive connection was reused after Local only: %v", err)
	}
}

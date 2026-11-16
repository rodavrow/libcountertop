package videoplay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
)

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
		if got := Local(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Local(%s) = %v, want %v", addr, got, want)
		}
	}
}

func guardResolving(addrs ...string) *guard {
	g := newGuard(nil)
	g.lookup = func(context.Context, string) ([]netip.Addr, error) {
		out := make([]netip.Addr, len(addrs))
		for i, a := range addrs {
			out[i] = netip.MustParseAddr(a)
		}
		return out, nil
	}
	return g
}

func TestAPrivateAddressIsAccepted(t *testing.T) {
	_, err := newGuard(nil).parse(context.Background(), "http://192.168.1.10:8099/a.mp4")
	if err != nil {
		t.Fatalf("a private address was refused: %v", err)
	}
}

func TestAPublicAddressIsRefused(t *testing.T) {
	_, err := newGuard(nil).parse(context.Background(), "https://93.184.215.14/a.mp4")
	if !errors.Is(err, ErrNotLocal) {
		t.Fatalf("a public address gave %v, want ErrNotLocal", err)
	}
}

func TestANameThatResolvesPubliclyIsRefused(t *testing.T) {
	_, err := guardResolving("93.184.215.14").parse(context.Background(), "https://example.com/a.mp4")
	if !errors.Is(err, ErrNotLocal) {
		t.Fatalf("a public name gave %v, want ErrNotLocal", err)
	}
}

func TestANameWithAnyPublicAddressIsRefused(t *testing.T) {
	_, err := guardResolving("192.168.1.10", "93.184.215.14").
		parse(context.Background(), "http://nas.lan/a.mp4")
	if !errors.Is(err, ErrNotLocal) {
		t.Fatalf("a name with one public address gave %v, want ErrNotLocal", err)
	}
}

func TestANameThatResolvesLocallyIsAccepted(t *testing.T) {
	_, err := guardResolving("192.168.1.10").parse(context.Background(), "http://nas.lan/a.mp4")
	if err != nil {
		t.Fatalf("a local name was refused: %v", err)
	}
}

func TestANameThatDoesNotResolveIsRefused(t *testing.T) {
	g := newGuard(nil)
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

func TestARedirectToAPublicAddressIsRefused(t *testing.T) {
	srv := redirector(t)
	err := get(t, newGuard(nil).client(nil), srv.URL+redirecting("http://93.184.215.14/a.mp4"))
	if !errors.Is(err, ErrNotLocal) {
		t.Fatalf("a redirect to a public address gave %v, want ErrNotLocal", err)
	}
}

func TestARedirectWithinTheNetworkIsFollowed(t *testing.T) {
	srv := redirector(t)
	if err := get(t, newGuard(nil).client(nil), srv.URL+redirecting(srv.URL+"/a.mp4")); err != nil {
		t.Fatalf("a local redirect was refused: %v", err)
	}
}

func TestTheDefaultClientRefusesToConnectOutside(t *testing.T) {
	srv := redirector(t)
	g := newGuard(func(netip.Addr) bool { return false })
	if err := get(t, g.client(nil), srv.URL); !errors.Is(err, ErrNotLocal) {
		t.Fatalf("connecting to a refused address gave %v, want ErrNotLocal", err)
	}
}

func TestAGivenClientChecksEveryRequest(t *testing.T) {
	srv := redirector(t)
	g := newGuard(func(netip.Addr) bool { return false })
	if err := get(t, g.client(&http.Client{}), srv.URL); !errors.Is(err, ErrNotLocal) {
		t.Fatalf("a given client reached a refused address: %v", err)
	}
	outside := srv.URL + redirecting("http://93.184.215.14/")
	if err := get(t, newGuard(nil).client(&http.Client{}), outside); !errors.Is(err, ErrNotLocal) {
		t.Fatalf("a given client followed a redirect outside: %v", err)
	}
}

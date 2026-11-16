package videoplay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

var ErrNotLocal = errors.New("videoplay: only addresses on the local network can play")

const (
	lookupWait   = 5 * time.Second
	maxRedirects = 10
)

// Local holds the private (RFC 1918, fc00::/7), loopback and link-local addresses.
func Local(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast()
}

type guard struct {
	allowed func(netip.Addr) bool
	lookup  func(ctx context.Context, host string) ([]netip.Addr, error)
}

func newGuard(allowed func(netip.Addr) bool) *guard {
	if allowed == nil {
		allowed = Local
	}
	lookup := func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	return &guard{allowed: allowed, lookup: lookup}
}

func (g *guard) parse(ctx context.Context, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("videoplay: the url does not parse: %w", err)
	}
	return u, g.check(ctx, u)
}

// check refuses a URL unless every address its host has is allowed, so a name that also points
// outside cannot slip through.
func (g *guard) check(ctx context.Context, u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("videoplay: only http and https URLs can play, not %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("videoplay: the url names no host")
	}
	addrs, err := g.resolve(ctx, host)
	if err != nil {
		return fmt.Errorf("videoplay: looking up %s: %w", host, err)
	}
	for _, a := range addrs {
		if !g.allowed(a.Unmap()) {
			return fmt.Errorf("%w: %s is %s", ErrNotLocal, host, a)
		}
	}
	return nil
}

func (g *guard) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, lookupWait)
	defer cancel()
	addrs, err := g.lookup(ctx, host)
	if err == nil && len(addrs) == 0 {
		err = errors.New("no addresses")
	}
	return addrs, err
}

func (g *guard) redirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("videoplay: stopped after %d redirects", maxRedirects)
	}
	return g.check(req.Context(), req.URL)
}

// dial refuses at connect time, which also covers a name that resolved locally when it was checked
// and elsewhere when it was dialed.
func (g *guard) dial(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("videoplay: dialing %s: %w", address, err)
	}
	if !g.allowed(ap.Addr().Unmap()) {
		return fmt.Errorf("%w: refused to connect to %s", ErrNotLocal, ap.Addr())
	}
	return nil
}

// client dials through the guard, with no proxy, when there is no base. A base keeps its own
// transport (an *http.Transport loses its proxy), so each request is checked before it goes.
func (g *guard) client(base *http.Client) *http.Client {
	if base == nil {
		d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: g.dial}
		return &http.Client{
			Transport: &http.Transport{
				DialContext:           d.DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          4,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 15 * time.Second,
			},
			CheckRedirect: g.redirect,
		}
	}
	c := *base
	next := base.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	if t, ok := next.(*http.Transport); ok {
		t = t.Clone()
		t.Proxy = nil
		next = t
	}
	c.Transport = checked{g: g, next: next}
	c.CheckRedirect = g.redirect
	return &c
}

type checked struct {
	g    *guard
	next http.RoundTripper
}

func (c checked) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := c.g.check(r.Context(), r.URL); err != nil {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, err
	}
	return c.next.RoundTrip(r)
}

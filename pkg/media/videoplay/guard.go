package videoplay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync/atomic"
	"syscall"
	"time"
)

var ErrBlocked = errors.New("blocked: Internet access is Local only")

const (
	lookupWait   = 5 * time.Second
	maxRedirects = 10
)

// local holds the private (RFC 1918, fc00::/7), loopback and link-local addresses.
func local(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast()
}

// guard keeps fetches on the local network while localOnly says so. It asks at every check,
// request and dial, so turning Local only on stops what is already playing at its next request.
type guard struct {
	localOnly func() bool
	local     func(netip.Addr) bool
	lookup    func(ctx context.Context, host string) ([]netip.Addr, error)
	refusals  atomic.Uint64
}

func newGuard(localOnly func() bool) *guard {
	lookup := func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	return &guard{localOnly: localOnly, local: local, lookup: lookup}
}

func (g *guard) restricted() bool { return g.localOnly != nil && g.localOnly() }

// refuse counts a fetch the guard stopped, so the player can tell a block from a broken stream.
func (g *guard) refuse(err error) error {
	if errors.Is(err, ErrBlocked) {
		g.refusals.Add(1)
	}
	return err
}

func (g *guard) parse(ctx context.Context, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("videoplay: the url does not parse: %w", err)
	}
	return u, g.check(ctx, u)
}

// check refuses, under Local only, a URL unless every address its host has is local, so a name
// that also points outside cannot slip through.
func (g *guard) check(ctx context.Context, u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("videoplay: only http and https URLs can play, not %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("videoplay: the url names no host")
	}
	if !g.restricted() {
		return nil
	}
	addrs, err := g.resolve(ctx, host)
	if err != nil {
		return fmt.Errorf("videoplay: looking up %s: %w", host, err)
	}
	for _, a := range addrs {
		if !g.local(a.Unmap()) {
			return fmt.Errorf("%w: %s is %s", ErrBlocked, host, a)
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
	return g.refuse(g.check(req.Context(), req.URL))
}

// dial refuses at connect time, which also covers a name that resolved locally when it was checked
// and elsewhere when it was dialed.
func (g *guard) dial(_, address string, _ syscall.RawConn) error {
	if !g.restricted() {
		return nil
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("videoplay: dialing %s: %w", address, err)
	}
	if !g.local(ap.Addr().Unmap()) {
		return g.refuse(fmt.Errorf("%w: refused to connect to %s", ErrBlocked, ap.Addr()))
	}
	return nil
}

// client never uses a proxy. Every request is checked before it goes, which also covers one that
// would reuse a connection opened before Local only was turned on. With no base it also dials
// through the guard; a base keeps its own transport.
func (g *guard) client(base *http.Client) *http.Client {
	if base == nil {
		d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: g.dial}
		return &http.Client{
			Transport: checked{g: g, next: &http.Transport{
				DialContext:           d.DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          4,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 15 * time.Second,
			}},
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
	if err := c.g.refuse(c.g.check(r.Context(), r.URL)); err != nil {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, err
	}
	return c.next.RoundTrip(r)
}

package avatar

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

// failedPolls is how many status calls in a row may fail before a fed reply counts as broken off;
// one is a hiccup on the network.
const failedPolls = 2

// Fed reports whether Home Assistant streams slot's replies into its session, so the reply may
// start before Home Assistant names the clip.
func (a *Avatars) Fed(slot int) bool {
	cfg, ok := a.slot(slot)
	return ok && cfg.Ready() && cfg.Fed
}

// speakFed follows a reply Home Assistant streams into the session itself, and the device
// speaks the rest of the clip at url when the stream breaks off. With no streamed reply open
// (a cached reply, an announcement, a chat line) the clip goes to the avatar as before.
func (a *Avatars) speakFed(
	ctx context.Context,
	l *link,
	cfg schema.Avatar,
	id string,
	gen uint64,
	url string,
	sink Sink,
	started func(),
) (handled bool, from time.Duration, err error) {
	srv := a.server(cfg)
	// Listen before deciding: a streamed reply may already be speaking.
	sp := newSpeech(sink, started, a.now)
	if a.attach(l, gen, sp) == nil {
		slog.Info("avatar session changed, speaking locally", "slot", l.slot+1)
		return false, 0, nil
	}

	reply := a.openReply(ctx, l, srv, id, url)
	if ctx.Err() != nil {
		sp.stop(true)
		a.detach(l, sp)
		interruptNow(srv, id, l.slot)
		return true, 0, nil
	}
	if reply == "" {
		sp.stop(false)
		a.detach(l, sp)
		slog.Info("no streamed avatar reply, sending the clip", "slot", l.slot+1)
		handled, err = a.speakClip(ctx, l, cfg, id, gen, url, sink, started)
		return handled, 0, err
	}
	defer a.detach(l, sp)
	l.follows(reply)
	slog.Info("avatar following the streamed reply", "slot", l.slot+1, "reply", reply)
	return a.followFed(ctx, l, srv, id, gen, reply, sp)
}

// openReply is the new streamed reply open on the session, or empty for none. It asks once the
// clip's first bytes arrive: ha-vam opens its stream before Home Assistant has them, so a reply
// not open by then is not fed at all. One already followed is an earlier reply's.
func (a *Avatars) openReply(ctx context.Context, l *link, srv server, id, url string) string {
	if err := a.firstBytes(ctx, url); err != nil && ctx.Err() == nil {
		slog.Warn("avatar backup clip", "slot", l.slot+1, "err", err)
	}
	if ctx.Err() != nil {
		return ""
	}
	pctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, reply, err := srv.status(pctx, id)
	if err != nil {
		slog.Warn("avatar status", "slot", l.slot+1, "err", err)
		return ""
	}
	if reply == l.lastFollowed() {
		return ""
	}
	return reply
}

// firstBytes waits until the clip at url starts to arrive.
func (a *Avatars) firstBytes(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, a.timing.backup)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := a.o.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	var one [1]byte
	_, err = io.ReadFull(resp.Body, one[:])
	return err
}

// followFed plays the streamed reply until LiveTalking closes it. When it breaks off, the device
// takes over from a little before where the voice stopped.
func (a *Avatars) followFed(
	ctx context.Context,
	l *link,
	srv server,
	id string,
	gen uint64,
	reply string,
	sp *speech,
) (handled bool, from time.Duration, err error) {
	opened := a.now()
	t := time.NewTicker(a.timing.poll)
	defer t.Stop()
	interrupt := func() { interruptNow(srv, id, l.slot) }
	brokeOff := func(why string) (bool, time.Duration, error) {
		from := max(sp.spoken()-a.timing.rewind, 0)
		sp.stop(true)
		interrupt()
		slog.Warn("avatar reply broke off, speaking the rest locally",
			"slot", l.slot+1, "why", why, "from", from)
		return false, from, nil
	}
	failed := 0
	for {
		select {
		case <-ctx.Done():
			sp.stop(true)
			interrupt()
			return true, 0, nil
		case <-t.C:
		}
		now := a.now()
		first, last := sp.heard()
		if a.lost(l, gen) {
			return brokeOff("session lost")
		}
		a.sync(l, sp)
		pctx, cancel := context.WithTimeout(ctx, time.Second)
		on, open, err := srv.status(pctx, id)
		cancel()
		if err != nil {
			if failed++; failed >= failedPolls {
				return brokeOff(err.Error())
			}
			continue
		}
		failed = 0
		switch {
		case first.IsZero() && now.Sub(opened) > a.timing.fedFirstSound:
			slog.Warn(
				"avatar sent no sound for the streamed reply, speaking locally",
				"slot",
				l.slot+1,
			)
			sp.stop(true)
			interrupt()
			return false, 0, nil
		case now.Sub(opened) > a.timing.most:
			interrupt()
			return true, 0, fmt.Errorf("avatar: still speaking after %s", a.timing.most)
		case open != reply:
			// LiveTalking closes the reply once its last frame has played; the tail may still be
			// on its way.
			if first.IsZero() {
				return false, 0, nil
			}
			if on || now.Sub(last) < a.timing.quiet {
				continue
			}
			return true, 0, nil
		case first.IsZero():
			continue
		case now.Sub(last) > a.timing.fedSilent:
			return brokeOff("silent")
		}
	}
}

package avatar

import (
	"context"
	"fmt"
	"log/slog"
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

// speakFed follows a reply Home Assistant streams into the session itself. When the stream breaks
// off, it returns the rest of the clip at url for the device to speak. With no streamed reply open
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
) (handled bool, rest []int16, err error) {
	srv := a.server(cfg)
	// Listen before deciding: a streamed reply may already be speaking.
	sp := newSpeech(sink, started, a.now)
	if a.attach(l, gen, sp) == nil {
		slog.Info("avatar session changed, speaking locally", "slot", l.slot+1)
		return false, nil, nil
	}

	b := a.fetchBackup(ctx, url, a.timing.backup)
	defer b.stop()
	reply := a.openReply(ctx, l, srv, id, b)
	if ctx.Err() != nil {
		sp.stop(true)
		a.detach(l, sp)
		interruptNow(srv, id, l.slot)
		return true, nil, nil
	}
	if reply == "" {
		sp.stop(false)
		a.detach(l, sp)
		b.stop()
		slog.Info("no streamed avatar reply, sending the clip", "slot", l.slot+1)
		handled, err = a.speakClip(ctx, l, cfg, id, gen, url, sink, started)
		return handled, nil, err
	}
	l.follows(reply)
	slog.Info("avatar following the streamed reply", "slot", l.slot+1, "reply", reply)
	handled, from, err := a.followFed(ctx, l, srv, id, gen, reply, sp)
	a.detach(l, sp)
	if handled {
		return true, nil, err
	}
	rest, err = b.rest(ctx, from)
	switch {
	case err == nil:
		return false, rest, nil
	case ctx.Err() != nil:
		return true, nil, nil
	case from == 0:
		// Nothing was said yet: the device fetches the clip again itself.
		slog.Warn("avatar backup clip", "slot", l.slot+1, "err", err)
		return false, nil, nil
	default:
		// What the avatar said is not said again, so the rest of this reply is lost.
		slog.Warn("avatar backup clip lost, the rest of the reply is not spoken",
			"slot", l.slot+1, "from", from, "err", err)
		return true, nil, err
	}
}

// openReply is the new streamed reply open on the session, or empty for none. It asks once the
// clip's first bytes arrive: ha-vam opens its stream before Home Assistant has them, so a reply
// not open by then is not fed at all. One already followed is an earlier reply's.
func (a *Avatars) openReply(ctx context.Context, l *link, srv server, id string, b *backup) string {
	select {
	case <-b.first:
	case <-ctx.Done():
		return ""
	}
	pctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	st, err := srv.status(pctx, id)
	if err != nil {
		slog.Warn("avatar status", "slot", l.slot+1, "err", err)
		return ""
	}
	if st.reply == l.lastFollowed() {
		return ""
	}
	return st.reply
}

// followFed plays the streamed reply until LiveTalking closes it. When it breaks off, from is how
// far into the clip the device should take over, a little before where the voice stopped.
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
	// held is when the reply was last seen paused at its source: its silence is not a dropout.
	var held time.Time
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
		st, err := srv.status(pctx, id)
		cancel()
		if err != nil {
			if failed++; failed >= failedPolls {
				return brokeOff(err.Error())
			}
			continue
		}
		failed = 0
		if st.holding && st.reply == reply {
			held = now
			continue
		}
		switch {
		case first.IsZero() && now.Sub(later(opened, held)) > a.timing.fedFirstSound:
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
		case st.reply != reply:
			// LiveTalking closes the reply once its last frame has played; the tail may still be
			// on its way.
			if first.IsZero() {
				return false, 0, nil
			}
			if st.on || now.Sub(last) < a.timing.quiet {
				continue
			}
			return true, 0, nil
		case first.IsZero():
			continue
		case now.Sub(later(last, held)) > a.timing.fedSilent:
			return brokeOff("silent")
		}
	}
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

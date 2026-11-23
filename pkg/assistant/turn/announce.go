package turn

import (
	"context"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/media/pcm"
)

type announcement struct {
	esphome.Announce

	id    uint64
	slot  int
	shown bool
}

func (t *Conversation) announce(a esphome.Announce) {
	if !t.animated() {
		t.announcePlain(a)
		return
	}
	slog.Info("announce", "text", a.Text, "start_conversation", a.StartConversation)

	an := &announcement{Announce: a, slot: t.announceSlot()}
	if !t.offer(event{kind: evAnnounce, announcement: an}) {
		t.report(false)
	}
}

// announcePlain plays an announcement without showing it, outside the conversation's own events.
func (t *Conversation) announcePlain(a esphome.Announce) {
	slog.Info("announce", "text", a.Text, "start_conversation", a.StartConversation)

	t.player.Sounding(true)
	claim := t.sound.Claim("announce", func(ctx context.Context, _ Speaker) error {
		for _, url := range []string{a.PreannounceMediaID, a.MediaID} {
			if url == "" {
				continue
			}
			if err := t.play(ctx, url, nil); err != nil {
				return err
			}
		}
		return nil
	})

	t.spawn("announce", func() {
		<-claim.Done()
		t.player.Sounding(false)

		if err := claim.Err(); err != nil {
			slog.Error("playing the announcement failed", "err", err)
		}
		t.report(claim.Err() == nil && !claim.Stopped())

		if a.StartConversation && !claim.Stopped() {
			t.begin(0)
		}
	})
}

func (c *Conversation) animated() bool {
	return c.options.Announcements != nil && c.options.Announcements.Animated()
}

func (c *Conversation) announceSlot() int {
	return max(c.options.Announcements.Slot(), 0)
}

// Spoken tells the conversation that another player starts or ends speech Home Assistant
// pushed, so the speech is shown as a reply.
func (c *Conversation) Spoken(on bool) {
	if !on {
		c.deliver(event{kind: evSpoken})
		return
	}
	if !c.animated() {
		return
	}
	c.post(event{kind: evSpoken, on: true, slot: c.announceSlot()})
}

// settle plays the announcement that waited for the turn to end.
func (c *Conversation) settle() {
	if c.waiting == nil || c.phase != phaseIdle || c.pending != nil {
		return
	}
	an := c.waiting
	c.waiting = nil
	c.say(an)
}

func (c *Conversation) say(an *announcement) {
	c.announceID++
	an.id = c.announceID
	id := an.id
	c.announcing = an

	c.player.Sounding(true)
	claim := c.sound.Claim("announce", func(ctx context.Context, _ Speaker) error {
		if an.PreannounceMediaID != "" {
			if err := c.play(ctx, an.PreannounceMediaID, nil); err != nil {
				return err
			}
		}
		if an.MediaID == "" {
			return nil
		}
		return c.play(ctx, an.MediaID, func() {
			c.post(event{kind: evAnnouncing, replyID: id})
		})
	})

	c.spawn("announce", func() {
		<-claim.Done()

		if err := claim.Err(); err != nil {
			slog.Error("playing the announcement failed", "err", err)
		}
		c.report(claim.Err() == nil && !claim.Stopped())
		c.deliver(event{kind: evAnnounced, replyID: id, stopped: claim.Stopped()})
	})
}

func (c *Conversation) announcingShown(id uint64) {
	an := c.announcing
	if an == nil || an.id != id || c.phase != phaseIdle {
		return
	}
	an.shown = true
	c.showReply(an.slot, an.Text)
}

func (c *Conversation) announced(id uint64, stopped bool) {
	an := c.announcing
	if an == nil || an.id != id {
		return
	}
	c.announcing = nil
	c.player.Sounding(false)
	if an.shown && c.phase == phaseIdle && !c.voiced {
		c.present()
	}

	if an.StartConversation && !stopped {
		c.begin(0)
	}
}

func (c *Conversation) spoken(on bool, slot int) {
	if !on {
		if !c.voiced {
			return
		}
		c.voiced = false
		if c.phase == phaseIdle && !c.announcementShown() {
			c.present()
		}
		return
	}
	if c.phase != phaseIdle || c.pending != nil || c.announcing != nil {
		return
	}
	c.voiced = true
	c.showReply(slot, "")
}

func (c *Conversation) showReply(slot int, text string) {
	c.options.Presentation.Show(Showing{Phase: phaseReplying, Slot: slot, Reply: text})
	c.options.Presentation.Phase(slot, phaseReplying)
}

func (c *Conversation) announcementShown() bool {
	return c.voiced || (c.announcing != nil && c.announcing.shown)
}

// hush ends an announcement for a wake word, as a wake word ends a reply.
func (c *Conversation) hush() {
	if c.announcing == nil && !c.voiced {
		return
	}
	slog.Info("announcement interrupted")
	c.sound.Silence()
	c.drop()
}

// drop forgets the announcement being played; whoever stopped its sound calls it.
func (c *Conversation) drop() {
	shown := c.announcementShown()
	if c.announcing != nil {
		c.player.Sounding(false)
	}
	c.announcing, c.voiced = nil, false
	if shown && c.phase == phaseIdle {
		c.present()
	}
}

// unsaid gives up the announcement that was waiting; Home Assistant waits to hear of it.
func (c *Conversation) unsaid() {
	if c.waiting == nil {
		return
	}
	slog.Info("announcement dropped before it played", "text", c.waiting.Text)
	c.waiting = nil
	c.spawn("announcement report", func() { c.report(false) })
}

func (t *Conversation) play(ctx context.Context, url string, started func()) error {
	samples, err := t.fetch(ctx, url, 16000)
	if err != nil {
		return err
	}
	slog.Debug("playing announcement", "samples", len(samples))

	if started != nil {
		started()
	}
	t.speaker.PlayVoice(samples)
	t.speaker.PlayVoice(make([]int16, 16000*pcm.Tail/1000))
	return nil
}

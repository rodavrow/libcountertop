package avatar

import (
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

// hold keeps the avatar that last spoke on screen between announcements, such as the lines of a
// chat, while Home Assistant has the "Avatar hold" switch on. It is off at boot and never saved.
type hold struct {
	mu sync.Mutex
	on bool
	// slot is the held avatar, -1 for none.
	slot int
	// next is that a later turn or announcement has arrived; the held avatar stays up until
	// that one's show begins, which can be a second or more after it arrives.
	next  bool
	until time.Time
	// speaking counts avatar replies under way; the safety timeout waits for them.
	speaking int
	sw       *esphome.Switch
}

// newHold makes the device's "Avatar hold" switch. Integrations find it by display name on the
// main device, so the name is an English literal that never changes or is translated.
func (a *Avatars) newHold() {
	a.hold.slot = -1
	a.hold.sw = &esphome.Switch{Base: esphome.Base{
		ObjectID: "avatar_hold",
		Name:     "Avatar hold",
		Icon:     "mdi:account-box-multiple",
	}}
	a.hold.sw.OnCommand = a.setHold
	a.hold.sw.Set(false)
}

// DeviceEntities are the avatar entities of the whole device, for its main device rather than
// a wake word's sub-device.
func (a *Avatars) DeviceEntities() []esphome.Entity { return []esphome.Entity{a.hold.sw} }

func (a *Avatars) setHold(on bool) {
	h := &a.hold
	h.mu.Lock()
	had := h.on && h.slot >= 0
	h.on = on
	if on {
		h.until = a.now().Add(a.timing.hold)
	} else {
		h.slot = -1
	}
	h.mu.Unlock()
	h.sw.Set(on)
	slog.Info("avatar hold", "on", on)
	a.poke()
	if had && !on {
		a.HeldChanged.Emit(struct{}{})
	}
}

// replying marks an avatar reply starting or ending, so the safety timeout cannot run out
// under a long line.
func (a *Avatars) replying(start bool) {
	h := &a.hold
	h.mu.Lock()
	defer h.mu.Unlock()
	if start {
		h.speaking++
	} else if h.speaking > 0 {
		h.speaking--
	}
}

// spoke holds slot's picture once an avatar reply it showed has ended, and restarts the hold's
// safety timeout.
func (a *Avatars) spoke(slot int) {
	h := &a.hold
	h.mu.Lock()
	if !h.on {
		h.mu.Unlock()
		return
	}
	changed := h.slot != slot
	h.slot, h.next, h.until = slot, false, a.now().Add(a.timing.hold)
	h.mu.Unlock()
	if changed {
		slog.Info("avatar held", "slot", slot+1)
		a.HeldChanged.Emit(struct{}{})
	}
}

func (a *Avatars) heldSlot() int {
	h := &a.hold
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.on {
		return -1
	}
	return h.slot
}

// arrived notes that a later turn or announcement has arrived for the avatar held, if any.
func (a *Avatars) arrived() {
	h := &a.hold
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.on && h.slot >= 0 {
		h.next = true
	}
}

// Began tells the avatars that slot's turn or announcement is on screen. A held avatar waiting
// for one is let go, as that show takes its place; when the show is another avatar's, the held
// picture stays up until that one has its own. A show the hold was made under, still up after
// its reply ended, changes nothing. Call it on every frame a show is drawn.
func (a *Avatars) Began(slot int) {
	h := &a.hold
	h.mu.Lock()
	held := -1
	if h.on && h.next {
		held = h.slot
	}
	h.mu.Unlock()
	if held < 0 || held >= len(a.links) {
		return
	}
	other := slot != held && slot >= 0 && slot < len(a.links)
	switch {
	case slot == held:
		// Its own show places it from this frame on; a held picture was last placed up to a
		// second ago.
		a.links[held].stays(a.now())
	case other && a.Wants(slot) && a.links[slot].ready(a.busy()):
		a.links[held].handOver(slot, a.now().Add(a.timing.pending))
	}
	a.letGo(held, "the next turn or announcement")
}

// letGo stops holding slot, if it is the one held, and reports whether it was.
func (a *Avatars) letGo(slot int, why string) bool {
	h := &a.hold
	h.mu.Lock()
	had := slot >= 0 && h.slot == slot
	if had {
		h.slot, h.next = -1, false
	}
	h.mu.Unlock()
	if had {
		slog.Info("avatar let go", "slot", slot+1, "why", why)
		a.poke()
		a.HeldChanged.Emit(struct{}{})
	}
	return had
}

// holdable reports that slot's avatar can still be shown held: its mode is on, its link is up and
// its picture is up or due.
func (a *Avatars) holdable(slot int) bool {
	return a.Wants(slot) && a.links[slot].showable(a.now(), a.busy(), a.timing.pending)
}

// Held reports the avatar held on screen between announcements. One that can no longer show, as
// its link dropped, its avatar mode went off, other video took the screen or its picture went,
// is let go at once.
func (a *Avatars) Held() (slot int, ok bool) {
	slot = a.heldSlot()
	if slot < 0 || slot >= len(a.links) {
		return -1, false
	}
	if a.holdable(slot) {
		return slot, true
	}
	a.letGo(slot, "it can no longer show")
	return -1, false
}

// Release lets the held avatar go, as a touch or the next turn does, and reports whether its
// picture was on screen. The switch stays on, so the next avatar reply is held again.
func (a *Avatars) Release() bool {
	slot := a.heldSlot()
	if slot < 0 || slot >= len(a.links) {
		return false
	}
	shown := a.links[slot].onScreen()
	return a.letGo(slot, "touched") && shown
}

// checkHold lets go of a held avatar that can no longer show, and turns the hold off once its
// safety timeout passes with no reply under way, so a chat that ended without turning it off
// cannot keep the avatar up.
func (a *Avatars) checkHold(now time.Time) {
	if slot := a.heldSlot(); slot >= 0 && slot < len(a.links) && !a.holdable(slot) {
		a.letGo(slot, "it can no longer show")
	}
	h := &a.hold
	h.mu.Lock()
	expired := h.on && h.speaking == 0 && !now.Before(h.until)
	had := expired && h.slot >= 0
	if expired {
		h.on, h.slot = false, -1
	}
	h.mu.Unlock()
	if !expired {
		return
	}
	slog.Info("avatar hold timed out")
	h.mu.Lock()
	// Home Assistant may have turned it back on meanwhile; that state is the one to show.
	off := !h.on
	h.mu.Unlock()
	if off {
		h.sw.Set(false)
	}
	if had {
		a.HeldChanged.Emit(struct{}{})
	}
}

// Package announcements holds how speech Home Assistant pushes to the device is shown:
// whether it is animated as a reply, and which assistant the next one speaks as.
package announcements

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const (
	Group setting.Group = "Announcements"

	domain = "assistants"

	// NextID is the select Home Assistant sets just before an announcement.
	NextID = "next_announcement_assistant"

	// Default leaves the next announcement to the first assistant.
	Default = "Default"

	// Kept is how long a choice waits for its announcement before it is forgotten.
	Kept = 30 * time.Second
)

type Options struct {
	Slots    int
	DeviceID uint32
	Read     func() bool
	Save     func(bool) error

	// Chosen hears an animated announcement's assistant being picked (on) and the choice being
	// used up, expiring or cleared (off), once each, so its avatar can get ready in time.
	Chosen func(slot int, on bool)
}

type Announcements struct {
	options  Options
	next     *esphome.Select
	controls *setting.Controls[schema.Wake]

	mu     sync.Mutex
	chosen int
	until  time.Time
	expiry *time.Timer
	told   int

	now func() time.Time
}

func New(options Options) *Announcements {
	a := &Announcements{options: options, now: time.Now}
	a.next = &esphome.Select{
		Base: esphome.Base{
			ObjectID: NextID,
			DeviceID: options.DeviceID,
			Name:     "Next announcement assistant",
			Icon:     "mdi:account-voice",
			Category: esphome.CategoryConfig,
		},
		Options: Choices(options.Slots),
	}
	a.next.Set(Default)
	a.next.OnCommand = a.choose
	a.controls = a.table()
	return a
}

// Choices are Default and one label for each assistant.
func Choices(slots int) []string {
	out := []string{Default}
	for n := range slots {
		out = append(out, label(n))
	}
	return out
}

func label(slot int) string { return fmt.Sprintf("Assistant %d", slot+1) }

func (a *Announcements) Name() string { return "announcements" }

// Entities are the Animate announcements switch and the select that names the next speaker.
func (a *Announcements) Entities() []esphome.Entity {
	return append(a.controls.Entities(), a.next)
}

func (a *Announcements) On() bool { return a.options.Read() }

func (a *Announcements) Set(on bool) error {
	err := a.options.Save(on)
	a.controls.Publish()
	return err
}

func (a *Announcements) Animated() bool { return a.options.Read() }

func (a *Announcements) choose(option string) {
	slot := 0
	for n := range a.options.Slots {
		if option == label(n) {
			slot = n + 1
		}
	}
	if slot == 0 && option != Default {
		slog.Warn("no such assistant for the next announcement", "value", option)
	}

	a.mu.Lock()
	released := a.forget()
	if slot == 0 {
		a.mu.Unlock()
		a.tell(released, false)
		return
	}
	a.chosen, a.until = slot, a.now().Add(Kept)
	a.expiry = time.AfterFunc(Kept, a.expire)
	a.next.Set(label(slot - 1))
	picked := 0
	if a.options.Chosen != nil && a.Animated() {
		a.told, picked = slot, slot
	}
	a.mu.Unlock()
	a.tell(released, false)
	a.tell(picked, true)
}

// tell passes a choice to Chosen; slot counts from 1 and 0 is none.
func (a *Announcements) tell(slot int, on bool) {
	if slot > 0 && a.options.Chosen != nil {
		a.options.Chosen(slot-1, on)
	}
}

// Slot is the assistant the next announcement speaks as, counted from 0. Asking uses the
// choice up, so the announcement after it is the first assistant's again.
func (a *Announcements) Slot() int {
	a.mu.Lock()
	slot := 0
	if a.chosen > 0 && a.chosen <= a.options.Slots && a.now().Before(a.until) {
		slot = a.chosen - 1
	}
	released := a.forget()
	a.mu.Unlock()
	a.tell(released, false)
	return slot
}

func (a *Announcements) expire() {
	a.mu.Lock()
	released := 0
	if a.chosen > 0 && !a.now().Before(a.until) {
		released = a.forget()
	}
	a.mu.Unlock()
	a.tell(released, false)
}

// forget clears the choice and returns the slot, from 1, whose Chosen must now hear off.
func (a *Announcements) forget() int {
	if a.expiry != nil {
		a.expiry.Stop()
		a.expiry = nil
	}
	a.chosen, a.until = 0, time.Time{}
	a.next.Set(Default)
	told := a.told
	a.told = 0
	return told
}

func (a *Announcements) Controls() *setting.Controls[schema.Wake] { return a.controls }

func (a *Announcements) table() *setting.Controls[schema.Wake] {
	return &setting.Controls[schema.Wake]{
		Table:  Table(),
		Device: a.options.DeviceID,
		Read:   func() schema.Wake { return schema.Wake{AnimateAnnouncements: a.options.Read()} },
		Save: func(row setting.Setting[schema.Wake], v string) error {
			at := schema.Wake{AnimateAnnouncements: a.options.Read()}
			if err := row.Write(&at, v); err != nil {
				return err
			}
			return a.options.Save(at.AnimateAnnouncements)
		},
	}
}

func Table() *setting.Table[schema.Wake] {
	return setting.NewTable(
		domain,
		[]setting.Group{Group},
		[]setting.Setting[schema.Wake]{
			setting.Flag(
				setting.Setting[schema.Wake]{
					Name:  "animate_announcements",
					Group: Group,
					Icon:  "mdi:bullhorn",
				},
				func(w *schema.Wake) *bool { return &w.AnimateAnnouncements },
			),
		},
		setting.Messages{Text: say.T, Missing: say.Missing},
	)
}

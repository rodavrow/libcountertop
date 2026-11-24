package avatar

import (
	"fmt"
	"log/slog"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

// entities are one wake word's avatar settings and status in Home Assistant. Integrations find
// them by display name within the assistant's sub-device, so the names and the connection
// options are English literals that never change and never go through a translated setting.
type entities struct {
	mode       *esphome.Switch
	url        *esphome.Text
	id         *esphome.Text
	connection *esphome.Select
	session    *esphome.TextSensor
	state      *esphome.TextSensor
}

func (a *Avatars) newEntities(n int) entities {
	write := func() schema.WakeWriter { return a.o.Write(n) }
	e := entities{
		mode: &esphome.Switch{Base: esphome.Base{
			ObjectID: fmt.Sprintf("avatar_mode_%d", n+1),
			Name:     "Avatar mode",
			Icon:     "mdi:account-box",
			Category: esphome.CategoryConfig,
		}},
		url: &esphome.Text{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("avatar_url_%d", n+1),
				Name:     "Avatar URL",
				Icon:     "mdi:link-variant",
				Category: esphome.CategoryConfig,
			},
			MaxLength: schema.MaxAvatarURL,
		},
		id: &esphome.Text{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("avatar_id_%d", n+1),
				Name:     "Avatar ID",
				Icon:     "mdi:card-account-details",
				Category: esphome.CategoryConfig,
			},
			MaxLength: schema.MaxAvatarID,
			Pattern:   "^[A-Za-z0-9_-]*$",
		},
		connection: &esphome.Select{
			Base: esphome.Base{
				ObjectID: fmt.Sprintf("avatar_connection_%d", n+1),
				Name:     "Avatar connection",
				Icon:     "mdi:lan-connect",
				Category: esphome.CategoryConfig,
			},
			Options: connectionLabels(),
		},
		session: &esphome.TextSensor{Base: esphome.Base{
			ObjectID: fmt.Sprintf("avatar_session_%d", n+1),
			Name:     "Avatar session",
			Icon:     "mdi:identifier",
			Category: esphome.CategoryDiagnostic,
		}},
		state: &esphome.TextSensor{Base: esphome.Base{
			ObjectID: fmt.Sprintf("avatar_state_%d", n+1),
			Name:     "Avatar state",
			Icon:     "mdi:access-point-network",
			Category: esphome.CategoryDiagnostic,
		}},
	}
	if a.o.DeviceID != nil {
		on := a.o.DeviceID(n)
		for _, b := range e.bases() {
			b.DeviceID = on
		}
	}

	e.mode.OnCommand = func(on bool) {
		if err := write().AvatarOn(on); err != nil {
			slog.Error("saving avatar mode failed", "slot", n+1, "err", err)
			return
		}
		e.mode.Set(on)
		slog.Info("avatar mode", "slot", n+1, "on", on)
		a.poke()
	}
	e.url.OnCommand = func(v string) {
		if err := write().AvatarURL(v); err != nil {
			slog.Warn("avatar url refused", "slot", n+1, "err", err)
			return
		}
		e.url.Set(v)
		a.poke()
	}
	e.id.OnCommand = func(v string) {
		if err := write().AvatarID(v); err != nil {
			slog.Warn("avatar id refused", "slot", n+1, "err", err)
			return
		}
		e.id.Set(v)
		a.poke()
	}
	e.connection.OnCommand = func(label string) {
		c, ok := connectionOf(label)
		if !ok {
			slog.Warn("unknown avatar connection", "slot", n+1, "value", label)
			return
		}
		if err := write().AvatarConnection(c); err != nil {
			slog.Error("saving the avatar connection failed", "slot", n+1, "err", err)
			return
		}
		e.connection.Set(c.Label())
		a.poke()
	}
	e.session.Set("")
	e.state.Set(string(Off))
	return e
}

func (e entities) bases() []*esphome.Base {
	return []*esphome.Base{
		&e.mode.Base, &e.url.Base, &e.id.Base, &e.connection.Base,
		&e.session.Base, &e.state.Base,
	}
}

// Entities are slot's avatar entities, for boards with a screen to add to the wake word's.
func (a *Avatars) Entities(slot int) []esphome.Entity {
	if slot < 0 || slot >= len(a.ents) {
		return nil
	}
	e := a.ents[slot]
	return []esphome.Entity{e.mode, e.url, e.id, e.connection, e.session, e.state}
}

// Restore shows the saved settings in Home Assistant.
func (a *Avatars) Restore(words []schema.WakeWord) {
	for n, e := range a.ents {
		saved := schema.DefaultWakeWord().Avatar
		if n < len(words) {
			saved = words[n].Avatar
		}
		e.mode.Set(saved.On)
		e.url.Set(saved.URL)
		e.id.Set(saved.ID)
		e.connection.Set(saved.Connection.Label())
	}
	a.poke()
}

func (a *Avatars) show(slot int, s State, session string) {
	if slot < 0 || slot >= len(a.ents) {
		return
	}
	a.ents[slot].state.Set(string(s))
	a.ents[slot].session.Set(session)
}

func connectionLabels() []string {
	var out []string
	for _, c := range schema.AvatarConnections() {
		out = append(out, c.Label())
	}
	return out
}

func connectionOf(label string) (schema.AvatarConnection, bool) {
	for _, c := range schema.AvatarConnections() {
		if c.Label() == label {
			return c, true
		}
	}
	return "", false
}

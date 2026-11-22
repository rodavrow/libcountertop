package volume

import (
	"slices"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type Levels interface {
	Level(schema.Stream) int
	Set(schema.Stream, int)
	Mute(schema.Stream, bool)
}
type Entities struct {
	Numbers  map[schema.Stream]*esphome.Number
	Switches map[schema.Stream]*esphome.Switch
	streams  []schema.Stream
}

func NewEntities(
	levels Levels,
	streams []schema.Stream,
	deviceID uint32,
) *Entities {
	e := &Entities{
		Numbers:  map[schema.Stream]*esphome.Number{},
		Switches: map[schema.Stream]*esphome.Switch{},
		streams:  slices.Clone(streams),
	}
	for _, s := range streams {
		n := &esphome.Number{
			Base: esphome.Base{
				ObjectID: "volume_" + string(s),
				Name:     s.Label() + " volume",
				Icon:     "mdi:volume-high",
				Category: esphome.CategoryConfig,
				DeviceID: deviceID,
			},
			Min:  0,
			Max:  100,
			Step: 1,
			Unit: "%",
			Mode: esphome.NumberSlider,
		}
		n.OnCommand = func(level float32) { levels.Set(s, int(level)) }
		e.Numbers[s] = n

		sw := &esphome.Switch{
			Base: esphome.Base{
				ObjectID: "mute_" + string(s),
				Name:     s.Label() + " mute",
				Icon:     "mdi:volume-off",
				Category: esphome.CategoryConfig,
				DeviceID: deviceID,
			},
		}
		sw.OnCommand = func(on bool) { levels.Mute(s, on) }
		sw.Set(false)
		e.Switches[s] = sw
	}
	return e
}

func (e *Entities) muted(s schema.Stream, on bool) {
	if sw := e.Switches[s]; sw != nil {
		sw.Set(on)
	}
}

func (e *Entities) Entities() []esphome.Entity {
	out := make([]esphome.Entity, 0, 2*len(e.streams))
	for _, s := range e.streams {
		out = append(out, e.Numbers[s])
	}
	for _, s := range e.streams {
		out = append(out, e.Switches[s])
	}
	return out
}

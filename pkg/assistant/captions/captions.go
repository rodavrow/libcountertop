// Package captions holds whether the display shows what an assistant heard and replies, as text.
package captions

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const (
	Group setting.Group = "Captions"

	domain = "assistants"
)

type Options struct {
	DeviceID uint32
	Read     func() bool
	Save     func(bool) error
}

type Captions struct {
	options  Options
	controls *setting.Controls[schema.Wake]
}

func New(options Options) *Captions {
	c := &Captions{options: options}
	c.controls = &setting.Controls[schema.Wake]{
		Table:  Table(),
		Device: options.DeviceID,
		Read:   func() schema.Wake { return schema.Wake{ShowCaptions: options.Read()} },
		Save: func(row setting.Setting[schema.Wake], v string) error {
			at := schema.Wake{ShowCaptions: options.Read()}
			if err := row.Write(&at, v); err != nil {
				return err
			}
			return options.Save(at.ShowCaptions)
		},
	}
	return c
}

func (c *Captions) Name() string { return "captions" }

// Entities is the Show captions switch.
func (c *Captions) Entities() []esphome.Entity { return c.controls.Entities() }

// On tells whether the heard and reply text is shown.
func (c *Captions) On() bool { return c.options.Read() }

func (c *Captions) Set(on bool) error {
	err := c.options.Save(on)
	c.controls.Publish()
	return err
}

func (c *Captions) Controls() *setting.Controls[schema.Wake] { return c.controls }

func Table() *setting.Table[schema.Wake] {
	return setting.NewTable(
		domain,
		[]setting.Group{Group},
		[]setting.Setting[schema.Wake]{
			setting.Flag(
				setting.Setting[schema.Wake]{
					Name:  "show_captions",
					Group: Group,
					Icon:  "mdi:closed-caption",
				},
				func(w *schema.Wake) *bool { return &w.ShowCaptions },
			),
		},
		setting.Messages{Text: say.T, Missing: say.Missing},
	)
}

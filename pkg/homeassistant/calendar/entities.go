package calendar

import (
	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const entitiesMost = 255

type (
	Setting = setting.Setting[schema.Calendar]
	Table   = setting.Table[schema.Calendar]
)

const Group setting.Group = "Calendar"

func NewTable() *Table {
	return setting.NewTable("calendar", []setting.Group{Group}, []Setting{
		setting.Words(
			Setting{Name: "entities", Group: Group, Icon: "mdi:calendar-multiple", NoEntity: true},
			entitiesMost,
			func(c *schema.Calendar) *string { return &c.Entities },
		),
		{
			Name:     "colors",
			Group:    Group,
			Icon:     "mdi:palette",
			NoEntity: true,
			Kind:     setting.Text,
			Read:     func(c *schema.Calendar) string { return schema.FormatPairs(c.Colors) },
			Write: func(c *schema.Calendar, v string) error {
				colors, err := schema.ParseColors(v)
				if err != nil {
					return err
				}
				c.Colors = colors
				return nil
			},
		},
		setting.Flag(
			Setting{Name: "weather", Group: Group, Icon: "mdi:weather-partly-cloudy"},
			func(c *schema.Calendar) *bool { return &c.Weather },
		),
		setting.Pick(
			Setting{Name: "opening", Group: Group, Icon: "mdi:calendar-start"},
			schema.CalendarOpenings(),
			func(c *schema.Calendar) *schema.CalendarOpening { return &c.Opening },
		),
		setting.Flag(
			Setting{Name: "saver", Group: Group, Icon: "mdi:calendar-clock"},
			func(c *schema.Calendar) *bool { return &c.Saver },
		),
	}, setting.Messages{Text: say.T, Missing: say.Missing})
}

func (c *Calendar) Name() string { return "calendar" }

func (c *Calendar) Entities() []esphome.Entity { return c.knobs.Entities() }

func (c *Calendar) Settings() *setting.Controls[schema.Calendar] { return c.knobs }

func (c *Calendar) Publish(s schema.Calendar) { c.knobs.PublishFrom(s) }

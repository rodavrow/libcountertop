package mediacard

import (
	"github.com/ygelfand/libcountertop/pkg/audio/volume"
	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

type Settings = setting.Controls[schema.Media]

func Table() *setting.Table[schema.Media] {
	return setting.NewTable(
		"media",
		[]setting.Group{volume.MediaGroup},
		[]setting.Setting[schema.Media]{
			setting.Pick(
				setting.Setting[schema.Media]{
					Name:  "player",
					Group: volume.MediaGroup,
					ID:    "player_on_start",
					Icon:  "mdi:play-box-outline",
				},
				schema.PlayerStarts(),
				func(m *schema.Media) *schema.PlayerStart { return &m.Player },
			),
			setting.Flag(
				setting.Setting[schema.Media]{
					Name:  "mini",
					Group: volume.MediaGroup,
					ID:    "mini_player",
					Icon:  "mdi:dock-bottom",
				},
				func(m *schema.Media) *bool { return &m.Mini },
			),
			setting.Flag(
				setting.Setting[schema.Media]{
					Name:  "background",
					Group: volume.MediaGroup,
					ID:    "background_play",
					Icon:  "mdi:headphones",
				},
				func(m *schema.Media) *bool { return &m.Background },
			),
		},
		setting.Messages{Text: say.T, Missing: say.Missing},
	)
}

func NewSettings(at storage.Section[schema.Media], device uint32, changed func()) *Settings {
	return &Settings{
		Table:  Table(),
		Device: device,
		Read:   at.Read,
		Save: func(s setting.Setting[schema.Media], v string) error {
			if err := setting.Store(at, s, v); err != nil {
				return err
			}
			changed()
			return nil
		},
	}
}

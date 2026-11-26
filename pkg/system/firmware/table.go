package firmware

import (
	"slices"

	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const Group setting.Group = "Updates"

func table(channels []string) *setting.Table[schema.Update] {
	options := make([]setting.Option, len(channels))
	for i, c := range channels {
		options[i] = setting.Option{Value: c, Label: c}
	}
	var t *setting.Table[schema.Update]
	t = setting.NewTable(
		"update",
		[]setting.Group{Group},
		[]setting.Setting[schema.Update]{{
			Name:    "channel",
			Group:   Group,
			ID:      "update_channel",
			Icon:    "mdi:source-branch",
			Kind:    setting.Choice,
			Options: options,
			Read:    func(u *schema.Update) string { return u.Channel },
			Write: func(u *schema.Update, v string) error {
				if !slices.Contains(channels, v) {
					return t.MustRow("channel").Bad(v, "")
				}
				u.Channel = v
				return nil
			},
		}},
		setting.Messages{Text: say.T, Missing: say.Missing},
	)
	return t
}

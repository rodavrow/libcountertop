package mealie

import (
	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

const Group setting.Group = "Mealie"

const textMost = 1024

type (
	Setting = setting.Setting[schema.Mealie]
	Table   = setting.Table[schema.Mealie]
)

func NewTable() *Table {
	token := setting.Words(
		Setting{Name: "token", Group: Group, Icon: "mdi:key"},
		textMost,
		func(c *schema.Mealie) *string { return &c.Token },
	)
	token.Kind = setting.Secret
	return setting.NewTable("mealie", []setting.Group{Group}, []Setting{
		setting.Words(
			Setting{Name: "server", Group: Group, Icon: "mdi:server"},
			textMost,
			func(c *schema.Mealie) *string { return &c.Server },
		),
		token,
	}, setting.Messages{Text: say.T, Missing: say.Missing})
}

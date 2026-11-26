package recipes

import (
	"log/slog"

	"github.com/ygelfand/libcountertop/pkg/display/menu"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/media/mealie"
	"github.com/ygelfand/libcountertop/pkg/say"
	setting "github.com/ygelfand/libcountertop/pkg/settings"
)

func (a *App) Page() *shell.Page { return SettingsPage(a.o.Kitchen) }

func SettingsPage(k *mealie.Kitchen) *shell.Page {
	return &shell.Page{
		Title: say.T("recipes.page"),
		Build: func() ([]menu.Row, []func(int)) {
			c := k.Settings()
			at := c.Read()
			rows := []menu.Row{{Label: say.T("recipes.status"), Value: k.Status().Says()}}
			acts := []func(int){nil}
			for _, s := range c.Table.Rows() {
				save := func(v string) {
					if err := c.Change(s.Name, v); err != nil {
						slog.Error(
							"saving a setting failed",
							"setting",
							"mealie."+s.Name,
							"err",
							err,
						)
					}
				}
				row := menu.Row{Label: s.Title(), Hint: s.Help(), Value: s.Read(&at), Save: save}
				row.Kind = menu.Field
				if s.Kind == setting.Secret {
					row.Kind = menu.Password
				}
				rows = append(rows, row)
				acts = append(acts, nil)
			}
			return rows, acts
		},
	}
}

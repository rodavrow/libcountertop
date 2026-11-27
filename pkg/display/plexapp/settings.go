package plexapp

import (
	"github.com/ygelfand/libcountertop/pkg/display/menu"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/media/plex"
	"github.com/ygelfand/libcountertop/pkg/say"
)

func (a *App) Page() *shell.Page { return SettingsPage(a.o.Plex, a.o.Shell) }

func SettingsPage(s *plex.Service, sh *shell.Shell) *shell.Page {
	return &shell.Page{
		Title: say.T("plex.app"),
		Build: func() ([]menu.Row, []func(int)) {
			rows := []menu.Row{{Label: say.T("plex.status"), Value: Says(s.Status())}}
			acts := []func(int){nil}
			if s.Status() == plex.SignedOut || s.Status() == plex.Signing {
				return rows, acts
			}
			rows = append(rows, menu.Row{
				Label: say.T("plex.profile.switch"), Kind: menu.Chevron,
			})
			acts = append(acts, func(int) {
				s.SwitchProfile()
				sh.Pop()
			})
			rows = append(rows, menu.Row{Label: say.T("plex.signout"), Kind: menu.Chevron})
			acts = append(acts, func(int) {
				s.SignOut()
				sh.Pop()
			})
			return rows, acts
		},
	}
}

func Says(st plex.Status) string {
	return say.T("plex.status." + [...]string{
		"signedout", "signing", "choosing", "connecting", "ready", "refused", "unreachable",
	}[st])
}

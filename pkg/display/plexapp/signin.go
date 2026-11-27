package plexapp

import (
	"errors"
	"strings"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/icons"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/media/plex"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
	"github.com/ygelfand/libcountertop/pkg/say"
)

const pinLength = 4

func (a *App) mark() gogui.View {
	st := gogui.CurrentTheme().TextStyleIconMedium
	st.Family, st.Size, st.Color = icons.Family, style.Reach()*1.6, progressGold
	return gogui.Label(icons.Glyph(icons.Named("mdi:plex")), st)
}

func (a *App) signedOut(w *gogui.Window) gogui.View {
	return a.o.UI.SignIn(w, widgets.SignIn{
		Mark:  a.mark(),
		Title: say.T("plex.signin.title"),
		Note:  say.T("plex.signin.hint"),
		Actions: []widgets.SignInAction{{
			ID: "plex-signin", Label: say.T("plex.signin"), Primary: true, Do: a.o.Plex.SignIn,
		}},
	})
}

func (a *App) signing(w *gogui.Window) gogui.View {
	pin, asking, problem := a.o.Plex.Code()
	page := widgets.SignIn{Mark: a.mark(), Title: say.T("plex.signin.title")}
	switch {
	case asking:
		page.Note = say.T("plex.asking")
	case problem != nil:
		page.Note = say.T("plex.signin.failed")
		if errors.Is(problem, plex.ErrExpired) {
			page.Note = say.T("plex.signin.expired")
		}
		page.Actions = append(page.Actions, widgets.SignInAction{
			ID: "plex-signin-again", Label: say.T("plex.signin.again"), Primary: true,
			Do: a.o.Plex.SignIn,
		})
	default:
		page.Go, page.Site, page.Enter = say.T(
			"plex.signin.go",
		), pin.URL, say.T(
			"plex.signin.enter",
		)
		page.Code, page.Waiting = pin.Code, say.T("plex.signin.waiting")
		page.Link = pin.QR
	}
	page.Actions = append(page.Actions, widgets.SignInAction{
		ID: "plex-signin-cancel", Label: say.T("common.actions.cancel"), Do: a.o.Plex.CancelSignIn,
	})
	return a.o.UI.SignIn(w, page)
}

func (a *App) choosing(w *gogui.Window) gogui.View {
	a.mu.Lock()
	picking, typed, wrong := a.picking, a.pin, a.wrong
	a.mu.Unlock()
	if picking != nil {
		return a.pinPad(*picking, typed, wrong)
	}
	profiles := a.o.Plex.Profiles()
	side := style.Reach() * 2.4
	tiles := make([]gogui.View, 0, len(profiles))
	for i, p := range profiles {
		tiles = append(tiles, a.profileTile(w, itoa(i), p, side))
	}
	head := gogui.CurrentTheme().TextStyleDisplay
	return centered(
		gogui.Label(say.T("plex.who"), head),
		gogui.Row(gogui.ContainerCfg{
			ID: "plex-profiles", Sizing: gogui.FitFit, Padding: gogui.NoPadding,
			Spacing: gogui.SpacingLarge, Content: tiles,
		}),
	)
}

func (a *App) profileTile(w *gogui.Window, id string, p plex.Profile, side float32) gogui.View {
	letter := gogui.CurrentTheme().TextStyleDisplay
	letter.Size = side * 0.45
	letter.Color = gogui.RGB(0xff, 0xff, 0xff)
	initial := ""
	if r := []rune(strings.TrimSpace(p.Title)); len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	face := gogui.Column(gogui.ContainerCfg{
		Width: side, Height: side, Sizing: gogui.FixedFixed, Padding: gogui.NoPadding,
		Radius: gogui.RadiusPx(side / 2), Color: progressGold,
		HAlign: gogui.HAlignCenter, VAlign: gogui.VAlignMiddle,
		Content: []gogui.View{gogui.Label(initial, letter)},
	})
	name := []gogui.View{}
	if p.Protected {
		st := widgets.Secondary()
		glyph := gogui.CurrentTheme().TextStyleIconMedium
		glyph.Family, glyph.Size, glyph.Color = icons.Family, st.Size, st.Color
		name = append(name, gogui.Label(icons.Glyph(icons.Named("mdi:lock")), glyph))
	}
	body := gogui.CurrentTheme().Cfg.TextStyleDef
	name = append(name, gogui.Label(widgets.Ellipsize(w, p.Title, body, side), body))
	return a.o.UI.Toolkit.Pressable(gogui.Column, gogui.ContainerCfg{
		ID: "plex-profile-" + id, Sizing: gogui.FitFit, Padding: gogui.PaddingSmall,
		Spacing: gogui.SpacingSmall, Radius: gogui.RadiusMedium, HAlign: gogui.HAlignCenter,
		Content: []gogui.View{face, gogui.Row(gogui.ContainerCfg{
			Sizing: gogui.FitFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingSmall,
			VAlign: gogui.VAlignMiddle, Content: name,
		})},
	}, func(e gogui.EventCtx) {
		a.pick(p)
		e.Consume()
	})
}

func (a *App) pick(p plex.Profile) {
	if !p.Protected {
		a.enter(p, "")
		return
	}
	a.mu.Lock()
	a.picking, a.pin, a.wrong = &p, "", false
	a.mu.Unlock()
	a.redraw()
}

func (a *App) enter(p plex.Profile, pin string) {
	safe.Go("plex profile", func() {
		err := a.o.Plex.Choose(p, pin)
		a.mu.Lock()
		if err == nil {
			a.picking = nil
		}
		a.pin, a.wrong = "", err != nil
		a.mu.Unlock()
		a.redraw()
	})
}

func (a *App) pinPad(p plex.Profile, typed string, wrong bool) gogui.View {
	dots := strings.Repeat("● ", len(typed)) + strings.Repeat("○ ", pinLength-len(typed))
	big := gogui.CurrentTheme().TextStyleDisplay
	hint := say.F("plex.pin", map[string]any{"Name": p.Title})
	if wrong {
		hint = say.T("plex.pin.wrong")
	}
	var rows []gogui.View
	for _, line := range []string{"123", "456", "789", "<0x"} {
		var keys []gogui.View
		for _, k := range line {
			keys = append(keys, a.key(k))
		}
		rows = append(rows, gogui.Row(gogui.ContainerCfg{
			Sizing: gogui.FitFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
			Content: keys,
		}))
	}
	return centered(append([]gogui.View{
		wrap(hint, widgets.Secondary()),
		gogui.Label(strings.TrimSpace(dots), big),
	}, rows...)...)
}

func (a *App) key(k rune) gogui.View {
	side := style.Reach() * 1.3
	st := gogui.CurrentTheme().TextStyleDisplay
	label := string(k)
	switch k {
	case '<':
		label = icons.Glyph(icons.Named("mdi:arrow-left"))
		st = gogui.CurrentTheme().TextStyleIconMedium
		st.Family = icons.Family
	case 'x':
		label = icons.Glyph(icons.Named("mdi:backspace-outline"))
		st = gogui.CurrentTheme().TextStyleIconMedium
		st.Family = icons.Family
	}
	return a.o.UI.Toolkit.Pressable(gogui.Column, gogui.ContainerCfg{
		ID: "plex-key-" + string(k), Width: side, Height: side, Sizing: gogui.FixedFixed,
		Padding: gogui.NoPadding, Radius: gogui.RadiusPx(side / 2),
		Color: gogui.CurrentTheme().Cfg.ColorPanel, HAlign: gogui.HAlignCenter,
		VAlign: gogui.VAlignMiddle, Content: []gogui.View{gogui.Label(label, st)},
	}, func(e gogui.EventCtx) {
		a.press(k)
		e.Consume()
	})
}

func (a *App) press(k rune) {
	a.mu.Lock()
	p := a.picking
	switch {
	case p == nil:
		a.mu.Unlock()
		return
	case k == '<':
		a.picking, a.pin, a.wrong = nil, "", false
	case k == 'x':
		if a.pin != "" {
			a.pin = a.pin[:len(a.pin)-1]
		}
	case len(a.pin) < pinLength:
		a.pin += string(k)
		a.wrong = false
	}
	full := a.picking != nil && len(a.pin) == pinLength
	pin := a.pin
	a.mu.Unlock()
	if full {
		a.enter(*p, pin)
	}
	a.redraw()
}

func (a *App) trouble(text string) gogui.View {
	return centered(
		wrap(text, widgets.Secondary()),
		gogui.Row(gogui.ContainerCfg{
			Sizing: gogui.FitFit, Padding: gogui.NoPadding, Spacing: gogui.SpacingMedium,
			Content: []gogui.View{
				a.chip(
					"plex-retry",
					"mdi:refresh",
					say.T("common.actions.retry"),
					true,
					a.o.Plex.Open,
				),
				a.chip("plex-signout", "mdi:logout", say.T("plex.signout"), false,
					a.o.Plex.SignOut),
			},
		}),
	)
}

func centered(views ...gogui.View) gogui.View {
	return gogui.Column(gogui.ContainerCfg{
		Sizing: gogui.FillFill, Padding: gogui.PaddingLarge, Spacing: gogui.SpacingLarge,
		HAlign: gogui.HAlignCenter, VAlign: gogui.VAlignMiddle, Content: views,
	})
}

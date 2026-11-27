package widgets

import (
	"strings"

	gogui "github.com/go-gui-org/go-gui/gui"
)

type SignIn struct {
	Mark    gogui.View
	Title   string
	Note    string
	Go      string
	Site    string
	Enter   string
	Code    string
	Link    string
	Waiting string
	Actions []SignInAction
}

type SignInAction struct {
	ID      string
	Label   string
	Primary bool
	Do      func()
}

func (u *UI) SignIn(w *gogui.Window, s SignIn) gogui.View {
	vw, vh := w.WindowSize()
	t := gogui.CurrentTheme().Cfg
	head := t.TextStyleDef
	head.Size *= 1.6
	quiet := Secondary()
	big := t.TextStyleDef
	big.Size *= 1.5
	code := t.TextStyleDef
	code.Size *= 3.2

	words := []gogui.View{}
	if s.Mark != nil {
		words = append(words, s.Mark)
	}
	words = append(words, gogui.Label(s.Title, head))
	var qr gogui.View
	if s.Note != "" {
		words = append(words, gogui.Text(gogui.TextCfg{
			Text: s.Note, TextStyle: quiet, Mode: gogui.TextModeWrap,
		}))
	}
	if s.Code != "" {
		words = append(words,
			gogui.Label(s.Go, quiet),
			gogui.Label(s.Site, big),
			gogui.Label(s.Enter, quiet),
			gogui.Label(strings.Join(strings.Split(s.Code, ""), " "), code),
			gogui.Label(s.Waiting, quiet),
		)
		if s.Link != "" {
			side := float32(min(vw, vh)) * 0.32
			qr = QR(s.Link, side, side)
		}
	}
	for _, a := range s.Actions {
		words = append(words, u.signInButton(a))
	}
	text := gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FitFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingMedium,
		HAlign:  gogui.HAlignCenter,
		Content: words,
	})
	content := []gogui.View{text}
	if qr != nil {
		content = append(content, qr)
	}
	cfg := u.Toolkit.Panel(gogui.ContainerCfg{
		Sizing:  gogui.FitFit,
		Padding: gogui.PaddingLarge,
		Spacing: gogui.SpacingLarge,
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Radius:  gogui.RadiusLarge,
		Content: content,
	})
	card := gogui.Column(cfg)
	if vw >= vh {
		card = gogui.Row(cfg)
	}
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Content: []gogui.View{card},
	})
}

func (u *UI) signInButton(a SignInAction) gogui.View {
	cfg := gogui.ContainerCfg{
		ID:     a.ID,
		Sizing: gogui.FitFit,
		Padding: gogui.NewPadding(
			gogui.PadMedium,
			gogui.PadLarge*2,
			gogui.PadMedium,
			gogui.PadLarge*2,
		),
		Radius: gogui.RadiusLarge,
		HAlign: gogui.HAlignCenter,
	}
	st := gogui.CurrentTheme().Cfg.TextStyleDef
	u.Toolkit.Tab(&cfg, &st, Chosen(a.Primary))
	cfg.Content = []gogui.View{gogui.Label(a.Label, st)}
	return u.Toolkit.Pressable(gogui.Row, cfg, func(e gogui.EventCtx) {
		a.Do()
		e.Consume()
	})
}

package voicematchview

import (
	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/assistant/voicematch"
	"github.com/ygelfand/libcountertop/pkg/display/interaction"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/theme"
	"github.com/ygelfand/libcountertop/pkg/display/ui"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/say"
)

const (
	ringShare  = 0.42
	ringWidest = 0.6
	edgeShare  = 0.07
	dotShare   = 0.012
	dotGap     = 3.2
)

func (v *View) build(w *gogui.Window) gogui.View {
	t := gogui.CurrentTheme().Cfg
	pal := v.o.UI.Palette()
	vw, vh := w.WindowSize()
	side := min(float32(vh)*ringShare, float32(vw)*ringWidest)
	top := (float32(vh) - side) / 2
	p := v.o.Voices.Progress()
	if b, ok := v.outcome(p); ok {
		v.halo.Close()
		return gogui.Column(gogui.ContainerCfg{
			Sizing:  gogui.FillFill,
			Padding: gogui.NoPadding,
			Color:   t.ColorBackground,
			Content: []gogui.View{v.o.UI.Toolkit.Busy(w, b, pal)},
		})
	}
	v.halo.Place(ui.Rect{W: vw, H: vh})

	reach := style.Reach()
	edge := max(float32(vh)*edgeShare, reach*0.5)
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NoPadding,
		Color:   gogui.ColorTransparent,
		Content: []gogui.View{
			gogui.Column(gogui.ContainerCfg{
				Height:  top,
				Sizing:  gogui.FillFixed,
				Padding: gogui.NewPadding(edge, reach, reach*0.4, reach),
				Spacing: gogui.SpacingMedium,
				HAlign:  gogui.HAlignCenter,
				VAlign:  gogui.VAlignTop,
				Content: v.heading(p, pal),
			}),
			gogui.Column(gogui.ContainerCfg{
				Height:  side,
				Sizing:  gogui.FillFixed,
				Padding: gogui.NoPadding,
			}),
			gogui.Column(gogui.ContainerCfg{
				Sizing:  gogui.FillFill,
				Padding: gogui.NewPadding(reach*0.4, reach, edge, reach),
				Spacing: gogui.SpacingLarge,
				HAlign:  gogui.HAlignCenter,
				VAlign:  gogui.VAlignBottom,
				Content: v.footer(w, p, pal),
			}),
		},
	})
}

func (v *View) heading(p voicematch.Progress, pal theme.Theme) []gogui.View {
	lead := gogui.CurrentTheme().Cfg.TextStyleDef
	lead.Size *= 1.3
	lead.Color = widgets.Color(pal.Text.Blend(pal.Background, 0.35))
	big := gogui.CurrentTheme().TextStyleTitle
	big.Size = style.Reach() * 0.95
	big.Color = widgets.Color(pal.Accent)

	if p.Step.Phrase == "" {
		return []gogui.View{gogui.Label(say.T("voicematch.preparing"), lead)}
	}
	line := say.T("voicematch.say_plain")
	if p.Wake != "" {
		line = say.F("voicematch.say", map[string]any{"Wake": p.Wake})
	}
	return []gogui.View{
		gogui.Label(line, lead),
		gogui.Label("“"+p.Step.Phrase+"”", big),
	}
}

func (v *View) outcome(p voicematch.Progress) (shell.Busy, bool) {
	cancel := shell.BusyButton{
		ID:    "voicematch-cancel",
		Label: say.T("common.actions.cancel"),
		Do:    v.o.Shell.Pop,
	}
	again := shell.BusyButton{
		ID:      "voicematch-again",
		Label:   say.T("common.actions.retry"),
		Primary: true,
		Do:      v.again,
	}
	switch p.Outcome {
	case voicematch.Ready:
		return shell.Busy{
			Text:    say.F("voicematch.done", map[string]any{"Name": p.Name}),
			Outcome: shell.Succeeded,
			Buttons: []shell.BusyButton{{
				ID:      "voicematch-done",
				Label:   say.T("voicematch.finish"),
				Primary: true,
				Do:      v.o.Shell.Pop,
			}},
		}, true
	case voicematch.Unconfirmed:
		return shell.Busy{
			Text:    say.T("voicematch.unconfirmed"),
			Outcome: shell.Failed,
			Buttons: []shell.BusyButton{cancel, again},
		}, true
	case voicematch.Failed:
		return shell.Busy{
			Text:    say.T("voicematch.failed"),
			Outcome: shell.Failed,
			Buttons: []shell.BusyButton{cancel, again},
		}, true
	case voicematch.Similar:
		return shell.Busy{
			Text:    say.F("voicematch.similar", map[string]any{"Name": p.Like}),
			Outcome: shell.Warned,
			Buttons: []shell.BusyButton{cancel, again, {
				ID:    "voicematch-accept",
				Label: say.T("voicematch.accept"),
				Do:    v.accept,
			}},
		}, true
	}
	return shell.Busy{}, false
}

func (v *View) footer(w *gogui.Window, p voicematch.Progress, pal theme.Theme) []gogui.View {
	status := gogui.CurrentTheme().Cfg.TextStyleDef
	status.Size *= 1.2
	status.Color = widgets.Color(pal.Text)
	return []gogui.View{
		dots(p.Step, pal, float32(min(w.WindowSize()))),
		gogui.Label(statusText(p.Step), status),
		v.key(w, "voicematch-cancel", say.T("common.actions.cancel"), false, pal, v.o.Shell.Pop),
	}
}

func statusText(st voicematch.Step) string {
	switch st.Stage {
	case voicematch.Heard:
		return say.T("voicematch.heard")
	case voicematch.Missed:
		return say.T("voicematch.missed")
	case voicematch.Prompt:
		return say.T("voicematch.listening")
	}
	return " "
}

func dots(st voicematch.Step, pal theme.Theme, short float32) gogui.View {
	total := st.Of + 1
	if st.Confirm {
		total = st.Of
	}
	done := st.Number - 1
	if st.Stage == voicematch.Heard {
		done = st.Number
	}
	r := max(short*dotShare, 4)
	gap := r * dotGap
	wide := gap*float32(total-1) + 2*r + 2
	tall := 2*r + 2
	return gogui.DrawCanvas(gogui.DrawCanvasCfg{
		Width:   wide,
		Height:  tall,
		Version: uint64(done)<<8 | uint64(st.Number)<<1 | uint64(st.Stage&1),
		OnDraw: func(dc *gogui.DrawContext) {
			for i := range total {
				x, y := 1+r+gap*float32(i), tall/2
				switch {
				case i < done:
					dc.FilledPolygon(interaction.Disc(x, y, r), widgets.Color(pal.Accent))
				case i == done:
					dc.FilledPolygon(interaction.Disc(x, y, r), widgets.Color(pal.Accent))
					dc.FilledPolygon(interaction.Disc(x, y, r*0.6), widgets.Color(pal.Background))
				default:
					dc.FilledPolygon(
						interaction.Disc(x, y, r),
						widgets.Color(pal.Text.Blend(pal.Background, 0.75)),
					)
				}
			}
		},
	})
}

func (v *View) key(
	w *gogui.Window,
	id, label string,
	primary bool,
	pal theme.Theme,
	do func(),
) gogui.View {
	st := gogui.CurrentTheme().TextStyleTitle
	fill, ink := pal.Background.Blend(pal.Text, 0.12), pal.Text
	if primary {
		fill, ink = pal.Accent, pal.Contrast(pal.Accent)
	}
	st.Color = widgets.Color(ink)
	tall := style.Reach()
	return v.o.UI.Toolkit.Pressable(gogui.Row, gogui.ContainerCfg{
		ID:      id,
		Width:   widgets.KeyWidth(w, label, st, tall) + tall,
		Height:  tall,
		Sizing:  gogui.FixedFixed,
		Radius:  gogui.RadiusLarge,
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Padding: gogui.NoPadding,
		Color:   widgets.Color(fill),
		Content: []gogui.View{gogui.Label(label, st)},
	}, func(e gogui.EventCtx) {
		do()
		e.Window.InvalidateLayout()
	})
}

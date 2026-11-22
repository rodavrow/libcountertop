package airplayview

import (
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/navigation"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/display/style"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/say"
)

type Code struct {
	client, digits string
}

func (*Code) Covers() bool { return true }

func (*Code) Timeout() time.Duration { return shell.AppTimeout }

func (*Code) Wakes() bool { return true }

type Options struct {
	UI    *widgets.UI
	Shell *shell.Shell
}

type View struct{ o Options }

func New(o Options) *View { return &View{o: o} }

func (v *View) Show(client, digits string) (release func()) {
	return v.o.Shell.Hold(&Code{client: client, digits: digits}).Release
}

func (v *View) Screen(sv shell.View) *navigation.Screen {
	c, ok := sv.(*Code)
	if !ok {
		return nil
	}
	return &navigation.Screen{
		Fixed: true,
		Clear: true,
		View:  c,
		Build: func(w *gogui.Window) gogui.View { return v.build(w, c) },
	}
}

func (v *View) build(w *gogui.Window, c *Code) gogui.View {
	t := gogui.CurrentTheme().Cfg
	vw, _ := w.WindowSize()
	reach := style.Reach()
	wide := min(float32(vw)-2*reach, reach*12)
	status := t.TextStyleDef
	status.Size *= 1.4
	icon := gogui.CurrentTheme().TextStyleIconMedium
	icon.Size = reach * 0.6
	return gogui.Column(gogui.ContainerCfg{
		Sizing:  gogui.FillFill,
		Padding: gogui.NewPadding(reach*0.5, reach, reach*0.5, reach),
		Spacing: gogui.SpacingLarge,
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Color:   t.ColorBackground,
		Content: []gogui.View{
			gogui.Label(say.F("airplay.code", map[string]any{"Client": c.client}), status),
			widgets.Code(c.digits),
			gogui.Row(gogui.ContainerCfg{
				Width:   min(wide, reach*6),
				Sizing:  gogui.FixedFit,
				Padding: gogui.NoPadding,
				Content: []gogui.View{
					v.o.UI.Toolkit.WideKey(
						"airplay-code-close",
						gogui.IconClose,
						icon,
						reach*1.4,
						style.Partial,
						func(gogui.EventCtx) { v.o.Shell.Remove(c) },
					),
				},
			}),
		},
	})
}

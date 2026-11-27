package widgets

import (
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"
)

const (
	SpinStep    = 100 * time.Millisecond
	spinnerKind = gogui.SvgSpinner6DotsRotate
	spinnerPart = 7
)

func SpinnerGlyph(side float32) gogui.View {
	return gogui.SvgSpinner(gogui.SvgSpinnerCfg{
		Kind:   spinnerKind,
		Width:  side,
		Height: side,
		Color:  gogui.CurrentTheme().Cfg.ColorAccent,
	})
}

func Spinner(vw, vh int) gogui.View {
	return gogui.Column(gogui.ContainerCfg{
		Width:   float32(vw),
		Height:  float32(vh),
		Sizing:  gogui.FixedFixed,
		Padding: gogui.NoPadding,
		HAlign:  gogui.HAlignCenter,
		VAlign:  gogui.VAlignMiddle,
		Content: []gogui.View{SpinnerGlyph(float32(min(vw, vh)) / spinnerPart)},
	})
}

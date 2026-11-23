package agenda

import (
	"math"
	"strconv"
	"time"

	gogui "github.com/go-gui-org/go-gui/gui"

	"github.com/ygelfand/libcountertop/pkg/display/weatherview"
	"github.com/ygelfand/libcountertop/pkg/display/widgets"
	"github.com/ygelfand/libcountertop/pkg/homeassistant/weather"
)

func (a *Agenda) forecast() map[time.Time]weather.Day {
	if a.o.Forecast == nil || !a.o.Calendar.Read().Weather {
		return nil
	}
	days := a.o.Forecast.Daily()
	out := make(map[time.Time]weather.Day, len(days))
	for _, d := range days {
		out[day(d.Date)] = d
	}
	return out
}

func degrees(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(int(math.Round(*v))) + "°"
}

func (a *Agenda) weatherTag(d weather.Day, size float32, low bool) gogui.View {
	text := widgets.Secondary()
	text.Size = size * 0.8
	temp := degrees(d.High)
	if low && d.Low != nil {
		temp += " / " + degrees(d.Low)
	}
	return gogui.Row(gogui.ContainerCfg{
		Sizing:  gogui.FitFit,
		Padding: gogui.NoPadding,
		Spacing: gogui.SpacingPx(size * 0.2),
		VAlign:  gogui.VAlignMiddle,
		Content: []gogui.View{
			gogui.Label(temp, text),
			weatherview.Icon(d.Condition, size, a.o.UI.Palette()),
		},
	})
}

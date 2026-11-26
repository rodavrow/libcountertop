package mealie

import (
	"github.com/ygelfand/libcountertop/pkg/runtime/product"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

func Build(p product.Context, settings storage.Section[schema.Mealie], timers Timers) *Kitchen {
	k := New(Options{
		Settings: settings,
		Device:   p.Screen,
		Timers:   timers,
		Redraw:   p.Redraw,
	})
	p.Settings.Add(k.Settings())
	return k
}

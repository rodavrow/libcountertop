package plex

import (
	"github.com/ygelfand/libcountertop/pkg/fetch"
	"github.com/ygelfand/libcountertop/pkg/runtime/product"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
	"github.com/ygelfand/libcountertop/pkg/settings/storage"
)

const platform = "Android"

func Build(
	p product.Context,
	settings storage.Section[schema.Plex],
	player PlayerOptions,
	device string,
) *Service {
	if player.HTTP == nil {
		player.HTTP = fetch.Client(0)
	}
	if player.Changed == nil {
		player.Changed = p.Redraw
	}
	return New(Options{
		Settings: settings,
		Identity: Identity{
			Product: p.Name, Version: p.Version, Device: p.Name, Name: device, Platform: platform,
		},
		HTTP:   fetch.Client(callTime),
		Player: player,
		Redraw: p.Redraw,
	})
}

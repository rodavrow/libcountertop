package cast

import (
	"context"
	"errors"
)

// ErrCloudOff is what a lookup returns when the receiver may not ask Google.
var ErrCloudOff = errors.New("cast: Google lookups are off")

// CloudAllowed reports whether a protocol may ask Google what a stock Cast device asks it, such
// as an app's receiver page. An Env without a Cloud switch may.
func (e Env) CloudAllowed() bool { return e.Cloud == nil || e.Cloud() }

// Describe is the package Describe through this Env's client, held back when Google lookups are
// off. A config already fetched is still returned, since that asks no one.
func (e Env) Describe(ctx context.Context, app string) (AppConfig, error) {
	if c, ok := knownConfig(app); ok {
		return c, nil
	}
	if !e.CloudAllowed() {
		return AppConfig{}, ErrCloudOff
	}
	return Describe(ctx, e.HTTP, app)
}

func knownConfig(app string) (AppConfig, bool) {
	configs.Lock()
	defer configs.Unlock()
	c, ok := configs.known[app]
	return c, ok
}

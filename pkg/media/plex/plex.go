package plex

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/ygelfand/plexgo"
	"github.com/ygelfand/plexgo/models/components"
	"github.com/ygelfand/plexgo/models/sdkerrors"
)

var (
	ErrToken    = errors.New("plex: the token was refused")
	ErrExpired  = errors.New("plex: the sign-in code expired")
	ErrPIN      = errors.New("plex: the profile PIN was refused")
	ErrNoServer = errors.New("plex: no server answered")
	ErrEmpty    = errors.New("plex: the library is empty")
)

type Identity struct {
	Client   string
	Product  string
	Version  string
	Device   string
	Name     string
	Platform string
}

func (id Identity) sdk(hc *http.Client, base, token string) *plexgo.PlexAPI {
	opts := []plexgo.SDKOption{
		plexgo.WithClient(hc),
		plexgo.WithClientIdentifier(id.Client),
		plexgo.WithProduct(id.Product),
		plexgo.WithVersion(id.Version),
		plexgo.WithDevice(id.Device),
		plexgo.WithDeviceName(id.Name),
		plexgo.WithPlatform(id.Platform),
		plexgo.WithAccepts(components.AcceptsApplicationJSON),
	}
	if base != "" {
		opts = append(opts, plexgo.WithServerURL(base))
	}
	if token != "" {
		opts = append(opts, plexgo.WithSecurity(token))
	}
	return plexgo.New(opts...)
}

func quiet(what string, err error) error {
	if err == nil {
		return nil
	}
	var (
		sdk     *sdkerrors.SDKError
		invalid *sdkerrors.ResponseValidationError
		denied  *sdkerrors.Unauthorized
		bad     *sdkerrors.BadRequest
	)
	switch {
	case errors.As(err, &denied):
		return ErrToken
	case errors.As(err, &bad):
		return fmt.Errorf("plex: %s: HTTP 400", what)
	case errors.As(err, &sdk):
		if e := status(sdk.StatusCode, what); e != nil {
			return e
		}
		return fmt.Errorf("plex: %s: HTTP %d", what, sdk.StatusCode)
	case errors.As(err, &invalid):
		return fmt.Errorf("plex: %s: unreadable reply, HTTP %d", what, invalid.StatusCode)
	}
	return fmt.Errorf("plex: %s: %w", what, err)
}

func status(code int, what string) error {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return ErrToken
	case code >= 300:
		return fmt.Errorf("plex: %s: HTTP %d", what, code)
	}
	return nil
}

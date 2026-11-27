package plex

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ygelfand/plexgo/models/operations"
)

const (
	linkURL   = "plex.tv/link"
	linkQR    = "https://www.plex.tv/link/?token="
	pinEvery  = 2 * time.Second
	pinLife   = 15 * time.Minute
	providesS = "server"
)

type PIN struct {
	ID      int64
	Code    string
	URL     string
	QR      string
	Expires time.Time
}

type Account struct {
	ID   Identity
	HTTP *http.Client
}

func (a Account) Ask(ctx context.Context) (PIN, error) {
	api := a.ID.sdk(a.HTTP, "", "")
	res, err := api.Authentication.CreateOAuthPin(ctx, operations.CreateOAuthPinRequest{},
		operations.CreateOAuthPinSecurity{ClientIdentifier: a.ID.Client})
	if err != nil {
		return PIN{}, quiet("asking for a sign-in code", err)
	}
	body := res.Object
	if body == nil || body.ID == nil || body.Code == nil {
		return PIN{}, errors.New("plex: asking for a sign-in code: empty reply")
	}
	life := pinLife
	if body.ExpiresIn != nil && *body.ExpiresIn > 0 {
		life = time.Duration(*body.ExpiresIn) * time.Second
	}
	return PIN{
		ID:      *body.ID,
		Code:    *body.Code,
		URL:     linkURL,
		QR:      linkQR + url.QueryEscape(*body.Code),
		Expires: time.Now().Add(life),
	}, nil
}

func (a Account) Await(ctx context.Context, pin PIN) (string, error) {
	ctx, cancel := context.WithDeadline(ctx, pin.Expires)
	defer cancel()
	api := a.ID.sdk(a.HTTP, "", "")
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "", ErrExpired
			}
			return "", ctx.Err()
		case <-time.After(pinEvery):
		}
		res, err := api.Authentication.GetOAuthPin(ctx,
			operations.GetOAuthPinRequest{PinID: pin.ID},
			operations.GetOAuthPinSecurity{ClientIdentifier: a.ID.Client})
		if err != nil {
			continue
		}
		if b := res.Object; b != nil && b.AuthToken != nil && *b.AuthToken != "" {
			return *b.AuthToken, nil
		}
	}
}

type Server struct {
	Name        string
	Machine     string
	Token       string
	Owned       bool
	Connections []Connection
}

type Connection struct {
	URI   string
	Local bool
	Relay bool
}

func (a Account) Servers(ctx context.Context, token string) ([]Server, error) {
	api := a.ID.sdk(a.HTTP, "", token)
	yes, relay, v6 := operations.IncludeHTTPSTrue, operations.IncludeRelayTrue, operations.IncludeIPv6False
	res, err := api.Plex.GetServerResources(ctx, operations.GetServerResourcesRequest{
		IncludeHTTPS: &yes, IncludeRelay: &relay, IncludeIPv6: &v6,
	})
	if err != nil {
		return nil, quiet("listing servers", err)
	}
	if err := status(res.StatusCode, "listing servers"); err != nil {
		return nil, err
	}
	var out []Server
	for _, d := range res.PlexDevices {
		if !strings.Contains(d.Provides, providesS) {
			continue
		}
		s := Server{Name: d.Name, Machine: d.ClientIdentifier, Token: d.AccessToken, Owned: d.Owned}
		for _, c := range d.Connections {
			s.Connections = append(
				s.Connections,
				Connection{URI: c.URI, Local: c.Local, Relay: c.Relay},
			)
		}
		out = append(out, s)
	}
	return out, nil
}

type Profile struct {
	UUID      string
	Title     string
	Thumb     string
	Admin     bool
	Protected bool
}

func (a Account) Profiles(ctx context.Context, token string) ([]Profile, error) {
	res, err := a.ID.sdk(a.HTTP, "", token).HomeUsers.GetHomeUsers(ctx)
	if err != nil {
		return nil, quiet("listing profiles", err)
	}
	if res.Object == nil {
		return nil, nil
	}
	out := make([]Profile, 0, len(res.Object.Users))
	for _, u := range res.Object.Users {
		out = append(out, Profile{
			UUID: u.UUID, Title: u.Title, Thumb: u.Thumb, Admin: u.Admin, Protected: u.Protected,
		})
	}
	return out, nil
}

func (a Account) Switch(ctx context.Context, token, uuid, pin string) (string, error) {
	req := operations.SwitchUserRequest{ID: uuid}
	if pin != "" {
		req.RequestBody.Pin = &pin
	}
	res, err := a.ID.sdk(a.HTTP, "", token).HomeUsers.SwitchUser(ctx, req)
	err = quiet("switching profile", err)
	if errors.Is(err, ErrToken) && pin != "" {
		return "", ErrPIN
	}
	if err != nil {
		return "", err
	}
	if res.UserPlexAccount == nil || res.UserPlexAccount.AuthToken == "" {
		return "", errors.New("plex: switching profile: no token in the reply")
	}
	return res.UserPlexAccount.AuthToken, nil
}

package plex

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const tryTime = 4 * time.Second

type answer struct {
	c    Connection
	took time.Duration
}

func (a Account) Connect(ctx context.Context, s Server) (*Library, error) {
	ctx, cancel := context.WithTimeout(ctx, tryTime)
	defer cancel()
	var (
		mu    sync.Mutex
		heard []answer
		wg    sync.WaitGroup
	)
	for _, c := range s.Connections {
		wg.Go(func() {
			began := time.Now()
			machine := a.identity(ctx, c.URI, s.Token)
			slog.Debug("plex address", "uri", c.URI, "local", c.Local, "relay", c.Relay,
				"took", time.Since(began), "answered", machine == s.Machine)
			if machine == s.Machine {
				mu.Lock()
				heard = append(heard, answer{c: c, took: time.Since(began)})
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(heard) == 0 {
		return nil, ErrNoServer
	}
	slices.SortFunc(heard, func(x, y answer) int {
		return cmp.Or(rank(x.c)-rank(y.c), cmp.Compare(x.took, y.took))
	})
	base := strings.TrimRight(heard[0].c.URI, "/")
	return &Library{
		ID: a.ID, HTTP: a.HTTP, Base: base, Token: s.Token, Machine: s.Machine,
		Local: heard[0].c.Local,
	}, nil
}

func rank(c Connection) int {
	switch {
	case c.Local && !c.Relay:
		return 0
	case !c.Relay:
		return 1
	}
	return 2
}

func (a Account) identity(ctx context.Context, base, token string) string {
	res, err := a.ID.sdk(a.HTTP, strings.TrimRight(base, "/"), token).General.GetIdentity(ctx)
	if err != nil || res.StatusCode != http.StatusOK || res.Object == nil ||
		res.Object.MediaContainer == nil {
		return ""
	}
	return deref(res.Object.MediaContainer.MachineIdentifier)
}

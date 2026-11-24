package cast

import (
	"context"
	"net/http"

	"github.com/ygelfand/libcountertop/pkg/media/cast/preferences"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
)

type Env struct {
	Name   string
	Model  string
	Device Device

	HTTP *http.Client
	Long *http.Client

	// Cloud says whether the receiver may ask Google about apps; nil means it may.
	Cloud func() bool

	Video func() playback.Target

	Volume        func() int
	Muted         func() bool
	SetVolume     func(level int)
	SetMuted      func(on bool)
	VolumeChanged func(do func()) (stop func())

	Publish   func(control Controls, p *Published)
	Send      func(m Message)
	Advertise func(app, name string, namespaces []string)
	End       func()
	Refuse    func(app string)
	Keep      func(app string) Kept

	Output playback.Output

	Resample func(from int) func(stereo []int16) []int16

	Preferences preferences.Provider

	Attest func(ctx context.Context, appID, sessionID, privateData, claimTemplate string) (map[string]string, error)
}

type Kept interface {
	Load(v any) bool
	Save(v any) error
	Clear() error
}

type Maker func(Env) Protocol

func Protocols(env Env, makers []Maker) []Protocol {
	out := make([]Protocol, 0, len(makers))
	for _, m := range makers {
		out = append(out, m(env))
	}
	return out
}

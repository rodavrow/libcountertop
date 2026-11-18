package schema

import (
	"encoding/json"

	"github.com/ygelfand/libcountertop/pkg/say"
)

type Network struct {
	Verify   bool     `json:"verify"`
	ViaHome  bool     `json:"via_home,omitempty"`
	Address  string   `json:"address,omitempty"`
	HomeKey  string   `json:"home_key,omitempty"`
	Internet Internet `json:"internet,omitempty"`
}

// Internet is how much of the internet the device may reach.
type Internet string

const (
	// InternetOpen changes nothing: every feature reaches what it needs.
	InternetOpen Internet = ""
	// InternetQuiet stops the lookups no feature needs: Google's Cast revocation list and app
	// configs, and the cast engine's own background fetches. Updates, photos and the like still
	// reach the internet.
	InternetQuiet Internet = "quiet"
	// InternetLocal is InternetQuiet with everything off the local network blocked as well.
	InternetLocal Internet = "local"
)

var Internets = []Internet{InternetOpen, InternetQuiet, InternetLocal}

func (i Internet) Label() string {
	switch i {
	case InternetQuiet:
		return say.T("network.internet.quiet")
	case InternetLocal:
		return say.T("network.internet.local")
	}
	return say.T("network.internet.open")
}

// Quiet reports whether background lookups are off.
func (i Internet) Quiet() bool { return i == InternetQuiet || i == InternetLocal }

// Local reports whether everything off the local network is blocked.
func (i Internet) Local() bool { return i == InternetLocal }

func DefaultNetwork() Network { return Network{Verify: true} }

func MigrateNetwork(data []byte, network *Network) error {
	var old struct {
		Network struct {
			Verify *bool `json:"verify"`
		} `json:"network"`
		Diag struct {
			InsecureTLS *bool `json:"insecure_tls"`
		} `json:"diag"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return err
	}
	if old.Network.Verify == nil && old.Diag.InsecureTLS != nil {
		network.Verify = !*old.Diag.InsecureTLS
	}
	return nil
}

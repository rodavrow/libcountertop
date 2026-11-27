package schema

type Plex struct {
	Client       string     `json:"client,omitempty"`
	Token        string     `json:"token,omitempty"`
	Profile      string     `json:"profile,omitempty"`
	ProfileToken string     `json:"profile_token,omitempty"`
	Server       string     `json:"server,omitempty"`
	LastServer   PlexServer `json:"last_server,omitzero"`
}

type PlexServer struct {
	Address string `json:"address,omitempty"`
	Token   string `json:"token,omitempty"`
	Local   bool   `json:"local,omitempty"`
}

func (p Plex) SignedIn() bool { return p.Token != "" }

func (p Plex) Account() string {
	if p.ProfileToken != "" {
		return p.ProfileToken
	}
	return p.Token
}

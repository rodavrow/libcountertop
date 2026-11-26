package schema

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Avatar is a wake word's LiveTalking avatar: its video stands in for the assistant's look and its
// lip-synced voice for the reply.
type Avatar struct {
	On         bool             `json:"on,omitempty"`
	URL        string           `json:"url,omitempty"`
	ID         string           `json:"id,omitempty"`
	Connection AvatarConnection `json:"connection,omitempty"`
	// Fed is that Home Assistant streams replies into the session itself, so the device follows
	// the session instead of sending it the whole clip.
	Fed bool `json:"fed,omitempty"`
}

// AvatarConnection is when the device keeps a LiveTalking session open.
type AvatarConnection string

const (
	AvatarWhenWoken AvatarConnection = "woken"
	AvatarAlways    AvatarConnection = "always"
)

const (
	MaxAvatarURL = 255
	MaxAvatarID  = 64
)

// Label is fixed English: Home Assistant integrations write these exact strings back, so they
// must not follow the device language.
func (c AvatarConnection) Label() string {
	if c == AvatarAlways {
		return "Always connected"
	}
	return "Connect when woken"
}

func AvatarConnections() []AvatarConnection {
	return []AvatarConnection{AvatarAlways, AvatarWhenWoken}
}

// Always reports whether the session stays open while avatar mode is on; unset means when woken.
func (c AvatarConnection) Always() bool { return c == AvatarAlways }

// Ready reports whether avatar mode is on with somewhere to connect to.
func (a Avatar) Ready() bool {
	return a.On && a.URL != "" && ValidAvatarURL(a.URL) == nil && ValidAvatarID(a.ID) == nil
}

var avatarID = regexp.MustCompile(`^[A-Za-z0-9_-]*$`)

// ValidAvatarID accepts LiveTalking avatar ids; empty means the server's default avatar.
func ValidAvatarID(id string) error {
	if len(id) > MaxAvatarID || !avatarID.MatchString(id) {
		return fmt.Errorf("avatar id %q: use up to %d letters, digits, _ or -", id, MaxAvatarID)
	}
	return nil
}

// ValidAvatarURL accepts http(s)://host[:port][/path] with no trailing slash, credentials, query
// or fragment, which is what the LiveTalking endpoints are appended to. Empty is allowed: avatar
// mode then stays on the normal path.
func ValidAvatarURL(raw string) error {
	if raw == "" {
		return nil
	}
	bad := func(why string) error { return fmt.Errorf("avatar url %q: %s", raw, why) }
	if len(raw) > MaxAvatarURL {
		return bad(fmt.Sprintf("longer than %d characters", MaxAvatarURL))
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return bad("not a URL")
	case u.Scheme != "http" && u.Scheme != "https":
		return bad("use http:// or https://")
	case u.Host == "" || u.Hostname() == "":
		return bad("no host")
	case u.User != nil:
		return bad("no user or password")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "?#"):
		return bad("no query or fragment")
	case strings.HasSuffix(raw, "/"):
		return bad("no trailing slash")
	}
	return nil
}

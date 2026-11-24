package schema

import (
	"strings"
	"testing"
)

func TestAvatarURLsAcceptAHostWithAnOptionalPortAndPath(t *testing.T) {
	for _, u := range []string{
		"",
		"http://192.168.1.87:8010",
		"https://avatar.lan",
		"http://avatar.lan/livetalking",
		"http://[fd00::1]:8010/a/b",
	} {
		if err := ValidAvatarURL(u); err != nil {
			t.Errorf("%q: %v", u, err)
		}
	}
}

func TestAvatarURLsRefuseWhatTheEndpointsCannotBeAppendedTo(t *testing.T) {
	for _, u := range []string{
		"192.168.1.87:8010",
		"ftp://avatar.lan",
		"http://",
		"http://avatar.lan/",
		"http://user:pw@avatar.lan",
		"http://avatar.lan?x=1",
		"http://avatar.lan?",
		"http://avatar.lan#top",
		"http://avatar.lan/" + strings.Repeat("a", MaxAvatarURL),
	} {
		if ValidAvatarURL(u) == nil {
			t.Errorf("%q is accepted", u)
		}
	}
}

func TestAvatarIDsAreShortWordsOrEmpty(t *testing.T) {
	for _, id := range []string{"", "sylvia", "Avatar_2-b"} {
		if err := ValidAvatarID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"has space", "slash/", "dot.", strings.Repeat("a", MaxAvatarID+1)} {
		if ValidAvatarID(id) == nil {
			t.Errorf("%q is accepted", id)
		}
	}
}

func TestAnAvatarIsReadyOnlyWhenOnWithAURL(t *testing.T) {
	cases := []struct {
		a    Avatar
		want bool
	}{
		{Avatar{}, false},
		{Avatar{On: true}, false},
		{Avatar{URL: "http://a.lan"}, false},
		{Avatar{On: true, URL: "http://a.lan/"}, false},
		{Avatar{On: true, URL: "http://a.lan", ID: "bad id"}, false},
		{Avatar{On: true, URL: "http://a.lan"}, true},
	}
	for _, c := range cases {
		if got := c.a.Ready(); got != c.want {
			t.Errorf("%+v ready = %v, want %v", c.a, got, c.want)
		}
	}
}

// Home Assistant integrations write these labels back, so they never follow the device language.
func TestAvatarConnectionLabelsAreTheAgreedEnglish(t *testing.T) {
	if got := AvatarAlways.Label(); got != "Always connected" {
		t.Errorf("always = %q", got)
	}
	if got := AvatarWhenWoken.Label(); got != "Connect when woken" {
		t.Errorf("when woken = %q", got)
	}
	if got := AvatarConnection("").Label(); got != "Connect when woken" {
		t.Errorf("unset = %q, want the default", got)
	}
}

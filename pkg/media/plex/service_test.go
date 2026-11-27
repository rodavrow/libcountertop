package plex

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type memory struct {
	mu sync.Mutex
	at schema.Plex
}

func (m *memory) Read() schema.Plex {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.at
}

func (m *memory) Update(change func(*schema.Plex)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(&m.at)
	return nil
}

func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSigningInChoosingAProfileAndBrowsing(t *testing.T) {
	a, _ := rig(t)
	settings := &memory{}
	s := New(Options{Settings: settings, Identity: Identity{Product: "Test"}, HTTP: a.HTTP})
	s.Open()
	if s.Status() != SignedOut {
		t.Fatalf("status %v before signing in, want signed out", s.Status())
	}
	s.SignIn()
	eventually(t, "the code", func() bool { pin, _, _ := s.Code(); return pin.Code == "WXYZ" })
	eventually(t, "the profile choice", func() bool { return s.Status() == Choosing })
	if settings.Read().Token != "account-token" || settings.Read().Client == "" {
		t.Errorf("saved %+v, want the account token and a client id", settings.Read())
	}
	kid := s.Profiles()[1]
	if err := s.Choose(kid, "0000"); !errors.Is(err, ErrPIN) {
		t.Errorf("wrong PIN gave %v, want ErrPIN", err)
	}
	if err := s.Choose(kid, "1234"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the connection", func() bool { return s.Status() == Ready })
	if got := settings.Read(); got.ProfileToken != "kid-token" || got.Server != "m1" {
		t.Errorf("saved %+v, want the profile token and the server", got)
	}
	eventually(t, "the home rows", func() bool { _, loaded, _ := s.Home(); return loaded })
	hubs, _, err := s.Home()
	if err != nil || len(hubs) == 0 || hubs[0].Items[0].RatingKey != "42" {
		t.Errorf("home %+v, %v", hubs, err)
	}
}

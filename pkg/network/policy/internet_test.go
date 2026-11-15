package policy

import (
	"testing"

	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

type networkMemory struct{ c schema.Network }

func (m *networkMemory) Read() schema.Network { return m.c }

func (m *networkMemory) Update(change func(*schema.Network)) error {
	change(&m.c)
	return nil
}

func TestInternetAccessIsOpenByDefault(t *testing.T) {
	p := New(&networkMemory{c: schema.DefaultNetwork()})
	if got := p.Internet(); got != schema.InternetOpen {
		t.Errorf("Internet() = %q, want open", got)
	}
	if p.Internet().Quiet() || p.Internet().Local() {
		t.Error("the default internet access blocks something")
	}
}

func TestInternetAccessTakesEachLevelByName(t *testing.T) {
	for _, tt := range []struct {
		value        string
		want         schema.Internet
		quiet, local bool
	}{
		{"quiet", schema.InternetQuiet, true, false},
		{"local", schema.InternetLocal, true, true},
		{"open", schema.InternetOpen, false, false},
	} {
		mem := &networkMemory{c: schema.DefaultNetwork()}
		p := New(mem)
		var changed []schema.Network
		p.Changed.Listen(func(n schema.Network) { changed = append(changed, n) })
		if err := p.Settings().Change("internet", tt.value); err != nil {
			t.Fatalf("Change(internet, %s) = %v", tt.value, err)
		}
		got := p.Internet()
		if got != tt.want || got.Quiet() != tt.quiet || got.Local() != tt.local {
			t.Errorf("after %s: Internet() = %q quiet=%v local=%v", tt.value, got, got.Quiet(),
				got.Local())
		}
		if len(changed) != 1 || changed[0].Internet != tt.want {
			t.Errorf("after %s: Changed carried %+v", tt.value, changed)
		}
	}
}

func TestInternetAccessRefusesAnUnknownLevel(t *testing.T) {
	p := New(&networkMemory{c: schema.DefaultNetwork()})
	if err := p.Settings().Change("internet", "partial"); err == nil {
		t.Error("Change(internet, partial) = nil, want an error")
	}
}

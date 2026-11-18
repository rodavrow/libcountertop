package settings

import (
	"testing"

	"github.com/ygelfand/libcountertop/pkg/display/menu"
	"github.com/ygelfand/libcountertop/pkg/display/shell"
	"github.com/ygelfand/libcountertop/pkg/say"
	config "github.com/ygelfand/libcountertop/pkg/settings/schema"
)

func networkRows(state *NetworkState, setInternet func(config.Internet)) []menu.Row {
	page := NetworkPage(
		func() NetworkState { return *state },
		func(bool) {}, func(bool) {}, setInternet, nil, nil, nil,
	)
	rows, _ := page.Build()
	return rows
}

func internetRow(rows []menu.Row) (menu.Row, bool) {
	for _, r := range rows {
		if r.Label == say.T("network.setting.internet") {
			return r, true
		}
	}
	return menu.Row{}, false
}

func TestTheNetworkPageShowsInternetAccess(t *testing.T) {
	state := &NetworkState{Internet: config.InternetLocal}
	row, ok := internetRow(networkRows(state, func(config.Internet) {}))
	if !ok {
		t.Fatal("the network page has no Internet access row")
	}
	if row.Kind != menu.Chevron || row.Value != config.InternetLocal.Label() {
		t.Errorf("the row is kind %v showing %q, want a chevron showing %q",
			row.Kind, row.Value, config.InternetLocal.Label())
	}
}

func TestPickingAnInternetAccessLevelSavesIt(t *testing.T) {
	state := &NetworkState{}
	var saved []config.Internet
	set := func(i config.Internet) {
		saved = append(saved, i)
		state.Internet = i
	}
	row, ok := internetRow(networkRows(state, set))
	if !ok {
		t.Fatal("the network page has no Internet access row")
	}
	picker := row.Open().(*shell.Page)
	rows, acts := picker.Build()
	if len(rows) != len(config.Internets) {
		t.Fatalf("the picker offers %d levels, want %d", len(rows), len(config.Internets))
	}
	if !rows[0].Chosen {
		t.Error("open is not marked as the current level")
	}
	acts[len(acts)-1](0)
	if len(saved) != 1 || saved[0] != config.InternetLocal {
		t.Fatalf("picking the last level saved %v, want local", saved)
	}
	if rows, _ = picker.Build(); !rows[len(rows)-1].Chosen {
		t.Error("the picker does not mark the level just saved")
	}
}

func TestWithoutASetterTheNetworkPageLeavesInternetAccessOut(t *testing.T) {
	if _, ok := internetRow(networkRows(&NetworkState{}, nil)); ok {
		t.Error("the network page offers Internet access with no way to save it")
	}
}

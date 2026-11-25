package captions

import (
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"
)

func fixture(on bool) (*Captions, *bool) {
	saved := on
	return New(Options{
		Read: func() bool { return saved },
		Save: func(v bool) error { saved = v; return nil },
	}), &saved
}

func TestTheSwitchReadsAndSavesTheSetting(t *testing.T) {
	c, saved := fixture(true)

	entities := c.Entities()
	if len(entities) != 1 {
		t.Fatalf("entities %+v, want the one switch", entities)
	}
	sw, ok := entities[0].(*esphome.Switch)
	if !ok || sw.ObjectID != "assistants_show_captions" || !sw.Get() {
		t.Fatalf("entity %+v, want the switch, on", entities[0])
	}

	sw.OnCommand(false)
	if *saved || c.On() {
		t.Fatal("switching off did not save")
	}
	if err := c.Set(true); err != nil || !*saved || !sw.Get() {
		t.Fatalf("Set(true): saved %v, switch %v, err %v", *saved, sw.Get(), err)
	}
}

func TestCaptionsStartAsTheyWereSaved(t *testing.T) {
	c, _ := fixture(false)

	if c.On() {
		t.Fatal("captions on, though saved off")
	}
}

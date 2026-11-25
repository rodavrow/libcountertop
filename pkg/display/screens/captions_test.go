package screens

import (
	"testing"

	voice "github.com/ygelfand/libcountertop/pkg/assistant/turn"
	"github.com/ygelfand/libcountertop/pkg/display/assistant"
)

func replying(captions func() bool) *App {
	as := assistant.New(assistant.Dependencies{})
	as.Show(voice.Showing{Phase: voice.Replying, Said: "what time is it", Reply: "half past nine"})
	return &App{o: Options{Assistant: as, Stage: &heldStage{}, Captions: captions}}
}

func TestCaptionsSwitchedOffHideWhatWasSaidAndTheReply(t *testing.T) {
	a := replying(func() bool { return false })

	show, ok := a.showing()

	if !ok || show.Said != "" || show.Reply != "" {
		t.Fatalf("showing %v with said %q and reply %q, want the show without its text",
			ok, show.Said, show.Reply)
	}
	if show.Phase != voice.Replying {
		t.Fatalf("phase %v, want the reply still shown", show.Phase)
	}
}

func TestCaptionsAreShownWhenSwitchedOn(t *testing.T) {
	a := replying(func() bool { return true })

	if show, _ := a.showing(); show.Said == "" || show.Reply == "" {
		t.Fatalf("said %q, reply %q, want both", show.Said, show.Reply)
	}
}

func TestCaptionsAreShownByAnAppWithoutTheSwitch(t *testing.T) {
	a := replying(nil)

	if show, _ := a.showing(); show.Said == "" || show.Reply == "" {
		t.Fatalf("said %q, reply %q, want both", show.Said, show.Reply)
	}
}

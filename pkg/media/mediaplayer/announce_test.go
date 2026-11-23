package mediaplayer

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/ygelfand/libcountertop/pkg/audio/sound"
)

// announced plays one announcement to its end and returns what Announcing heard.
func announced(t *testing.T, url string) []bool {
	t.Helper()
	var (
		mu    sync.Mutex
		heard []bool
	)
	s := &speaker{}
	driver := sound.NewDriver(s)
	fetched, quiet := make(chan struct{}), make(chan struct{}, 1)

	p := New(Options[*speaker]{
		Media:    s,
		Sound:    driver,
		Rate:     48000,
		Channels: 2,
		Volume:   volume{},
		Announcing: func(on bool) {
			mu.Lock()
			defer mu.Unlock()
			heard = append(heard, on)
		},
	})
	p.fetch = func(context.Context, string, int) ([]int16, error) {
		close(fetched)
		return []int16{1}, nil
	}
	p.notify = func() {
		if !p.speaking.Load() {
			quiet <- struct{}{}
		}
	}

	p.play(url, true)
	<-fetched
	// Ending the claim here spares the test the driver's wait for the speaker to run dry.
	driver.Silence()
	<-quiet

	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(heard)
}

func TestASpokenAnnouncementTellsWhoeverShowsIt(t *testing.T) {
	got := announced(t, "http://ha.local:8123/api/tts_proxy/abc123.wav")

	if !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("Announcing heard %v, want on then off", got)
	}
}

func TestAChimeAnnouncementIsNotShown(t *testing.T) {
	got := announced(t, "http://ha.local:8123/local/doorbell.wav")

	if len(got) != 0 {
		t.Fatalf("Announcing heard %v, want nothing", got)
	}
}

func TestOnlyHomeAssistantsSpeechCountsAsSpeech(t *testing.T) {
	for url, want := range map[string]bool{
		"http://ha.local:8123/api/tts_proxy/abc123.wav": true,
		"https://ha.example/api/tts_proxy/x.mp3?a=b":    true,
		"http://ha.local:8123/local/chime.wav":          false,
		"http://ha.local:8123/media/local/ding.mp3":     false,
		"": false,
	} {
		if got := Speech(url); got != want {
			t.Errorf("Speech(%q) = %v, want %v", url, got, want)
		}
	}
}

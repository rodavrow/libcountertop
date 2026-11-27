package activity

import (
	"sync"
	"testing"
	"time"
)

func TestAKnownSpeakerRidesAlongWithTheTurn(t *testing.T) {
	log, got := caught()
	turn := log.Begin(1, "Hey Google")
	turn.Awaits()
	turn.Spoke("Alex", "person.alex", 0.613)
	turn.Ends(Completed)
	if len(*got) != 1 {
		t.Fatalf("%d events, want 1", len(*got))
	}
	e := (*got)[0]
	if e["speaker"] != "Alex" || e["speaker_person"] != "person.alex" ||
		e["speaker_score"] != "0.61" {
		t.Errorf("speaker fields %v", e)
	}
	if e["version"] != Version {
		t.Errorf("version %q, want %q", e["version"], Version)
	}
}

func TestAnUnrecognizedVoiceIsUnknownWithItsScore(t *testing.T) {
	log, got := caught()
	turn := log.Begin(1, "Hey Google")
	turn.Awaits()
	turn.Spoke("", "", 0.42)
	turn.Ends(Completed)
	e := (*got)[0]
	if e["speaker"] != unknown || e["speaker_score"] != "0.42" || e["speaker_person"] != "" {
		t.Errorf("speaker fields %v", e)
	}
}

func TestATurnNobodyMatchesCarriesNoSpeaker(t *testing.T) {
	log, got := caught()
	log.Begin(1, "Hey Google").Ends(Completed)
	if _, ok := (*got)[0]["speaker"]; ok {
		t.Errorf("a turn with no voice match named a speaker: %v", (*got)[0])
	}
}

func TestALateMatchIsWaitedFor(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]string
	sent := make(chan struct{}, 2)
	log := New(Options{Emit: func(f map[string]string) {
		mu.Lock()
		got = append(got, f)
		mu.Unlock()
		sent <- struct{}{}
	}})
	turn := log.Begin(1, "Hey Google")
	turn.Awaits()
	turn.Ends(Completed)
	select {
	case <-sent:
		t.Fatal("the event went out before the awaited match")
	case <-time.After(100 * time.Millisecond):
	}
	turn.Spoke("Alex", "person.alex", 0.7)
	<-sent
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0]["speaker"] != "Alex" {
		t.Errorf("events %v, want one naming Alex", got)
	}
}

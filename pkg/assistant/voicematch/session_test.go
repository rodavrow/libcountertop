package voicematch

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
)

func TestSaveReplacesTheSameVoiceAndRemoveDropsIt(t *testing.T) {
	s := newService(t)
	ann := Profile{ID: "a", Name: "Ann", Threshold: Default, Prints: [][]float32{{1, 0}}}
	bo := Profile{ID: "b", Name: "Bo", Threshold: Default, Prints: [][]float32{{0, 1}}}
	for _, p := range []Profile{ann, bo, {ID: "a", Name: "Ann", Threshold: Default, Prints: [][]float32{{0.5, 0.5}}}} {
		if err := s.Save(p); err != nil {
			t.Fatal(err)
		}
	}
	got := s.Profiles()
	if len(got) != 2 {
		t.Fatalf("profiles %+v, want Ann once and Bo", got)
	}
	for _, p := range got {
		if p.ID == "a" && p.Prints[0][0] != 0.5 {
			t.Errorf("Ann kept the old takes: %+v", p)
		}
	}
	if err := s.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if got := s.Profiles(); len(got) != 1 || got[0].ID != "b" {
		t.Errorf("after removing Ann: %+v", got)
	}
}

func TestThresholdsOutsideTheRangeAreRefused(t *testing.T) {
	s := newService(t)
	for _, th := range []float64{0, Loosest - 0.01, Strictest + 0.01} {
		p := Profile{ID: "a", Name: "Ann", Threshold: th, Prints: [][]float32{{1, 0}}}
		if err := s.SetProfiles([]Profile{p}); err == nil {
			t.Errorf("threshold %.2f was accepted", th)
		}
	}
}

func TestASavedVoiceWithoutAThresholdIsNotLoaded(t *testing.T) {
	s := newService(t)
	raw := `[{"id":"a","name":"Ann","prints":[[1,0]]},` +
		`{"id":"b","name":"Bo","prints":[[0,1]],"threshold":0.5}]`
	if err := os.WriteFile(s.o.Profiles, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := NewService(s.o).Profiles(); len(got) != 1 || got[0].ID != "b" {
		t.Errorf("loaded %+v, want only Bo", got)
	}
}

func TestEachVoiceIsJudgedByItsOwnThreshold(t *testing.T) {
	s := newService(t)
	_ = s.SetProfiles([]Profile{
		{ID: "a", Name: "Ann", Threshold: Loosest, Prints: [][]float32{{1, 0, 0}}},
		{ID: "b", Name: "Bo", Threshold: Strictest, Prints: [][]float32{{0, 1, 0}}},
	})
	s.heard(time.Now(), nil, []float32{0.35, 0.3, 0.89})
	if got := s.speaker.Get(); got != "Ann" {
		t.Errorf("a 0.35 match against Ann at %.2f gave %q, want Ann", Loosest, got)
	}
	s.heard(time.Now(), nil, []float32{0.3, 0.6, 0.74})
	if got := s.speaker.Get(); got != unknown {
		t.Errorf("a 0.60 match against Bo at %.2f gave %q, want %q", Strictest, got, unknown)
	}
	s.heard(time.Now(), nil, []float32{0, 0.8, 0.6})
	if got := s.speaker.Get(); got != "Bo" {
		t.Errorf("a 0.80 match against Bo at %.2f gave %q, want Bo", Strictest, got)
	}
}

func TestTheTurnThatAskedHearsItsOwnAnswer(t *testing.T) {
	s := newService(t)
	_ = s.SetProfiles([]Profile{
		{
			ID:        "a",
			Name:      "Ann",
			Person:    "person.ann",
			Threshold: Default,
			Prints:    [][]float32{{1, 0}},
		},
	})
	if !s.Spoke(time.Time{}, nil) {
		t.Fatal("with a profile enrolled, a match should be coming")
	}
	at := time.Now()
	var name, person string
	var score float64
	s.mu.Lock()
	s.asked[at] = func(n, p string, sc float64, _ error) { name, person, score = n, p, sc }
	s.mu.Unlock()
	s.heard(at.Add(time.Second), nil, []float32{1, 0})
	if name != "" {
		t.Errorf("a match for another wake answered this turn with %q", name)
	}
	s.heard(at, nil, []float32{1, 0.1})
	if name != "Ann" || person != "person.ann" || score < 0.99 {
		t.Errorf("answer %q %q %.2f, want Ann", name, person, score)
	}
	var failed error
	s.mu.Lock()
	s.asked[at] = func(_, _ string, _ float64, err error) { failed = err }
	s.mu.Unlock()
	s.missed(at, ErrNoSpeech)
	if !errors.Is(failed, ErrNoSpeech) {
		t.Errorf("a failed match answered %v, want %v", failed, ErrNoSpeech)
	}
	empty := newService(t)
	if empty.Spoke(at, nil) {
		t.Error("with no profiles, no match should be promised")
	}
}

func TestTheWakeLabelLosesItsAssistantName(t *testing.T) {
	for label, want := range map[string]string{
		"Gemini: Hey Google": "Hey Google",
		"Alexa":              "Alexa",
		"":                   "",
	} {
		if got := spoken(label); got != want {
			t.Errorf("spoken(%q) = %q, want %q", label, got, want)
		}
	}
}

func TestAVoiceIsKeyedByItsPersonOrItsName(t *testing.T) {
	if got := IDFor("  Mary  Ann ", ""); got != "mary_ann" {
		t.Errorf("IDFor a typed name = %q, want mary_ann", got)
	}
	if got := IDFor("Mary", "person.mary"); got != "person.mary" {
		t.Errorf("IDFor a person = %q, want person.mary", got)
	}
}

func TestEveryChangeReachesHomeAssistantAsFields(t *testing.T) {
	s := newService(t)
	var sent []map[string]string
	s.o.Emit = func(f map[string]string) { sent = append(sent, f) }
	s = NewService(s.o)
	s.update(func(p *Progress) {
		*p = Progress{ID: "person.ann", Name: "Ann", Wake: "Alexa", Outcome: Enrolling}
	})
	s.update(func(p *Progress) {
		p.Step = Step{Number: 2, Of: 5, Phrase: "lock the front door", Stage: Heard}
	})
	s.update(func(p *Progress) { p.Outcome, p.Score = Ready, 0.71 })
	s.Cancel()
	want := []map[string]string{
		{"stage": "preparing", "voice": "person.ann", "name": "Ann", "person": "", "wake": "Alexa"},
		{
			"stage": "heard", "voice": "person.ann", "name": "Ann", "person": "", "wake": "Alexa",
			"take": "2", "of": "5", "confirm": "false", "phrase": "lock the front door",
		},
		{
			"stage": "ready", "voice": "person.ann", "name": "Ann", "person": "", "wake": "Alexa",
			"take": "2", "of": "5", "confirm": "false", "phrase": "lock the front door",
			"score": "0.710",
		},
		{"stage": "idle"},
	}
	if !reflect.DeepEqual(sent, want) {
		t.Errorf("sent %v\nwant %v", sent, want)
	}
}

func TestEnrollingNeedsAName(t *testing.T) {
	c, err := esphome.NewCall(map[string]any{"name": "  ", "person": "", "voice": ""})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := named(t, newService(t), "voice_enroll").Run(c); err == nil {
		t.Error("an enrolment with no name was started")
	}
}

func named(t *testing.T, s *Service, name string) *esphome.Action {
	t.Helper()
	for _, a := range s.Actions() {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no %s action", name)
	return nil
}

func held(t *testing.T, s *Service) {
	t.Helper()
	p := Profile{ID: "bo", Name: "Bo", Threshold: Default, Prints: [][]float32{{1, 0}}}
	s.mu.Lock()
	s.held = p
	s.mu.Unlock()
	s.update(func(pr *Progress) {
		*pr = Progress{ID: p.ID, Name: p.Name, Like: "Ann", Outcome: Similar}
	})
}

func TestAcceptingKeepsTheHeldVoice(t *testing.T) {
	s := newService(t)
	held(t, s)
	if got := s.Progress().fields(); got["stage"] != "similar" || got["like"] != "Ann" {
		t.Errorf("fields %v, want similar to Ann", got)
	}
	if err := s.Accept(); err != nil {
		t.Fatal(err)
	}
	if got := s.Profiles(); len(got) != 1 || got[0].ID != "bo" {
		t.Errorf("profiles %+v, want Bo", got)
	}
	if got := s.Progress(); got.Outcome != Ready || got.Like != "" {
		t.Errorf("progress %+v, want ready", got)
	}
	if err := s.Accept(); !errors.Is(err, ErrNothingHeld) {
		t.Errorf("a second accept returned %v, want %v", err, ErrNothingHeld)
	}
}

func TestCancellingDropsTheHeldVoice(t *testing.T) {
	s := newService(t)
	held(t, s)
	s.Cancel()
	if err := s.Accept(); !errors.Is(err, ErrNothingHeld) {
		t.Errorf("accept after cancel returned %v, want %v", err, ErrNothingHeld)
	}
	if got := s.Profiles(); len(got) != 0 {
		t.Errorf("profiles %+v, want none", got)
	}
}

func TestCancelWithoutAnEnrolmentClearsTheResult(t *testing.T) {
	s := newService(t)
	var seen []Progress
	s.Changed.Listen(func(p Progress) { seen = append(seen, p) })
	s.update(func(p *Progress) { *p = Progress{ID: "a", Outcome: Unconfirmed} })
	s.Cancel()
	if got := s.Progress(); got.Outcome != 0 {
		t.Errorf("progress after cancel %+v, want none", got)
	}
	if len(seen) != 2 || seen[1].Outcome != 0 {
		t.Errorf("changes %+v, want the result then the clear", seen)
	}
}

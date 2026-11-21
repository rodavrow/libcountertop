package voicematch

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
	"strings"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

var ErrEnrolling = errors.New("voicematch: an enrolment is already running")

type Outcome int

const (
	Enrolling Outcome = iota + 1
	Ready
	Unconfirmed
	Failed
	Similar
)

type Progress struct {
	ID      string
	Name    string
	Person  string
	Wake    string
	Like    string
	Step    Step
	Outcome Outcome
	Score   float64
	Err     error
}

func IDFor(name, person string) string {
	if person != "" {
		return person
	}
	return strings.Join(strings.Fields(strings.ToLower(name)), "_")
}

func (s *Service) Begin(id, name, person string) error {
	s.mu.Lock()
	phrase := s.wake
	s.mu.Unlock()
	wake := ""
	if phrase != nil {
		wake = spoken(phrase())
	}
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return ErrEnrolling
	}
	ctx, stop := context.WithCancel(context.Background())
	s.stop = stop
	s.held = Profile{}
	s.progress = Progress{ID: id, Name: name, Person: person, Wake: wake, Outcome: Enrolling}
	s.mu.Unlock()
	s.Changed.Emit(s.Progress())

	safe.Go("voice enrolment", func() {
		defer stop()
		p, m, err := s.Enroll(ctx, id, name, person, func(st Step) {
			s.update(func(pr *Progress) { pr.Step = st })
		})
		if err == nil {
			err = s.Save(p)
		}
		s.mu.Lock()
		s.stop = nil
		if errors.Is(err, ErrSimilar) {
			s.held = p
		}
		s.mu.Unlock()
		if errors.Is(err, context.Canceled) {
			s.update(func(pr *Progress) { *pr = Progress{} })
			return
		}
		s.update(func(pr *Progress) {
			pr.Score, pr.Err = m.Score, err
			switch {
			case err == nil:
				pr.Outcome = Ready
			case errors.Is(err, ErrNotConfirmed):
				pr.Outcome = Unconfirmed
			case errors.Is(err, ErrSimilar):
				pr.Outcome, pr.Like = Similar, m.Profile.Name
			default:
				pr.Outcome = Failed
			}
		})
		slog.Info(
			"voice enrolment ended",
			"name", name,
			"matched", m.Profile.Name,
			"score", m.Score,
			"err", err,
		)
	})
	return nil
}

func (s *Service) Progress() Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.progress
}

func (s *Service) Cancel() {
	s.mu.Lock()
	stop := s.stop
	s.mu.Unlock()
	if stop != nil {
		stop()
		return
	}
	s.mu.Lock()
	s.held = Profile{}
	s.mu.Unlock()
	s.update(func(pr *Progress) { *pr = Progress{} })
}

func (s *Service) Accept() error {
	s.mu.Lock()
	held := s.held
	if s.progress.Outcome != Similar || held.ID == "" {
		s.mu.Unlock()
		return ErrNothingHeld
	}
	s.held = Profile{}
	s.mu.Unlock()
	err := s.Save(held)
	s.update(func(pr *Progress) {
		pr.Like, pr.Err = "", err
		pr.Outcome = Ready
		if err != nil {
			pr.Outcome = Failed
		}
	})
	return err
}

// Save stores a voice and queues it for Home Assistant. Saving one exactly as it already stands
// does neither: every queued save carries the whole voice.
func (s *Service) Save(p Profile) error {
	if slices.ContainsFunc(s.Profiles(), func(q Profile) bool { return reflect.DeepEqual(q, p) }) {
		return nil
	}
	profiles := slices.DeleteFunc(s.Profiles(), func(q Profile) bool { return q.ID == p.ID })
	if err := s.SetProfiles(append(profiles, p)); err != nil {
		return err
	}
	return s.queue(change{Save: &p})
}

func (s *Service) Remove(id string) error {
	if err := s.SetProfiles(
		slices.DeleteFunc(s.Profiles(), func(q Profile) bool { return q.ID == id }),
	); err != nil {
		return err
	}
	return s.queue(change{Remove: id})
}

func (s *Service) update(change func(*Progress)) {
	s.mu.Lock()
	change(&s.progress)
	p := s.progress
	s.mu.Unlock()
	s.Changed.Emit(p)
}

func (p Progress) fields() map[string]string {
	out := map[string]string{"stage": p.stage()}
	if p.Outcome == 0 {
		return out
	}
	out["voice"], out["name"], out["person"], out["wake"] = p.ID, p.Name, p.Person, p.Wake
	if p.Step.Of > 0 {
		out["take"] = strconv.Itoa(p.Step.Number)
		out["of"] = strconv.Itoa(p.Step.Of)
		out["confirm"] = strconv.FormatBool(p.Step.Confirm)
		out["phrase"] = p.Step.Phrase
	}
	if p.Outcome != Enrolling {
		out["score"] = strconv.FormatFloat(p.Score, 'f', 3, 64)
	}
	if p.Like != "" {
		out["like"] = p.Like
	}
	return out
}

func (p Progress) stage() string {
	switch p.Outcome {
	case Ready:
		return "ready"
	case Unconfirmed:
		return "unconfirmed"
	case Failed:
		return "failed"
	case Similar:
		return "similar"
	case Enrolling:
		switch p.Step.Stage {
		case Prompt:
			return "prompt"
		case Heard:
			return "heard"
		case Missed:
			return "missed"
		}
		return "preparing"
	}
	return "idle"
}

func (s *Service) enrolActions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name:        "voice_enroll",
			Description: "Start enrolling a voice on this device. Progress arrives as esphome.echolocal_enrolment events.",
			Answers:     true,
			Args: []esphome.Arg{
				{
					Name:        "name",
					Type:        esphome.ArgString,
					Description: "The name to show for this voice.",
				},
				{
					Name:        "person",
					Type:        esphome.ArgString,
					Description: "The Home Assistant person this voice belongs to, or empty.",
				},
				{
					Name:        "voice",
					Type:        esphome.ArgString,
					Description: "The id of a voice to record again, or empty for a new one.",
				},
			},
			Run: func(c esphome.Call) (any, error) {
				return s.start(c.String("name"), c.String("person"), c.String("voice"))
			},
		},
		{
			Name:        "voice_enroll_cancel",
			Description: "Stop the voice enrolment running on this device.",
			Run: func(esphome.Call) (any, error) {
				s.Cancel()
				return nil, nil
			},
		},
		{
			Name:        "voice_enroll_accept",
			Description: "Keep a voice that sounds like an existing one.",
			Run: func(esphome.Call) (any, error) {
				return nil, s.Accept()
			},
		},
	}
}

func (s *Service) start(name, person, id string) (map[string]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("voice enrolment: a name is required")
	}
	if id == "" {
		id = IDFor(name, person)
	}
	if err := s.Begin(id, name, person); err != nil {
		return nil, err
	}
	return s.Progress().fields(), nil
}

func spoken(label string) string {
	if i := strings.LastIndex(label, ": "); i >= 0 {
		return label[i+2:]
	}
	return label
}

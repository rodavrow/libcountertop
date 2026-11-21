package voicematch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

const (
	exchange  = "echolocal.voices"
	reply     = "{{ response | to_json }}"
	syncLimit = 30 * time.Second
	// batchBytes is how much queued JSON one request carries, well inside what a connection takes.
	batchBytes = 32 << 10
)

type Asker interface {
	Ask(
		ctx context.Context,
		action string,
		data, templates map[string]string,
		tmpl string,
	) (json.RawMessage, error)
}

type change struct {
	Save   *Profile `json:"save,omitempty"`
	Remove string   `json:"remove,omitempty"`

	// seq tells a queued change from a later one for the same voice, so a sync clears only what
	// it sent.
	seq uint64
}

func (c change) voice() string {
	if c.Save != nil {
		return c.Save.ID
	}
	return c.Remove
}

// latest keeps the last change for each voice. A save carries the whole voice, so an earlier
// change for the same one adds nothing but size.
func latest(changes []change) []change {
	last := make(map[string]int, len(changes))
	for i, c := range changes {
		last[c.voice()] = i
	}
	out := make([]change, 0, len(last))
	for i, c := range changes {
		if last[c.voice()] == i {
			out = append(out, c)
		}
	}
	return out
}

func (s *Service) Sync(ctx context.Context) error {
	if s.o.Home == nil {
		return nil
	}
	s.syncing.Lock()
	defer s.syncing.Unlock()

	sent := 0
	for {
		batch, raw, more, err := s.batch()
		if err != nil {
			return err
		}
		profiles, err := s.exchange(ctx, raw)
		if err != nil {
			return err
		}
		// Home Assistant's list lacks whatever is still queued, so only the last answer is whole.
		if !more {
			if err := s.SetProfiles(profiles); err != nil {
				return err
			}
		}
		if err := s.delivered(batch); err != nil {
			return err
		}
		sent += len(batch)
		if !more {
			slog.Info("voices synced", "sent", sent, "voices", len(profiles))
			return nil
		}
	}
}

// batch takes queued changes from the front until one more would pass batchBytes, and always at
// least one. more reports whether any were left behind.
func (s *Service) batch() (batch []change, raw []byte, more bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	batch, raw = []change{}, []byte("[]")
	for i, c := range s.outbox {
		grown, err := json.Marshal(append(batch, c))
		if err != nil {
			return nil, nil, false, err
		}
		if i > 0 && len(grown) > batchBytes {
			return batch, raw, true, nil
		}
		batch, raw = append(batch, c), grown
	}
	return batch, raw, false, nil
}

func (s *Service) exchange(ctx context.Context, changes []byte) ([]Profile, error) {
	answer, err := s.o.Home.Ask(
		ctx,
		exchange,
		map[string]string{"changes": string(changes)},
		nil,
		reply,
	)
	if err != nil {
		return nil, fmt.Errorf("voicematch: syncing voices: %w", err)
	}
	var got struct {
		Profiles []Profile `json:"profiles"`
	}
	if err := json.Unmarshal(answer, &got); err != nil {
		return nil, fmt.Errorf("voicematch: the household's voices: %w", err)
	}
	return got.Profiles, nil
}

func (s *Service) delivered(batch []change) error {
	sent := make(map[uint64]bool, len(batch))
	for _, c := range batch {
		sent[c.seq] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outbox = slices.DeleteFunc(s.outbox, func(c change) bool { return sent[c.seq] })
	return s.keepOutbox()
}

func (s *Service) Resync() {
	s.syncs.Add(1)
	safe.Go("voice sync", func() {
		defer s.syncs.Done()
		ctx, cancel := context.WithTimeout(context.Background(), syncLimit)
		defer cancel()
		if err := s.Sync(ctx); err != nil {
			slog.Warn("voices not synced", "err", err)
		}
	})
}

func (s *Service) queue(c change) error {
	s.mu.Lock()
	s.outbox = latest(append(s.outbox, s.numbered(c)))
	err := s.keepOutbox()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.Resync()
	return nil
}

func (s *Service) loadOutbox() {
	raw, err := os.ReadFile(s.o.Outbox)
	switch {
	case errors.Is(err, os.ErrNotExist):
		for _, p := range s.profiles {
			s.outbox = append(s.outbox, s.numbered(change{Save: &p}))
		}
		if err := s.keepOutbox(); err != nil {
			slog.Warn("voice outbox not written", "err", err)
		}
	case err != nil:
		slog.Warn("voice outbox unreadable", "path", s.o.Outbox, "err", err)
	default:
		var kept []change
		if err := json.Unmarshal(raw, &kept); err != nil {
			slog.Warn("voice outbox unreadable", "path", s.o.Outbox, "err", err)
			return
		}
		for _, c := range latest(kept) {
			s.outbox = append(s.outbox, s.numbered(c))
		}
		if len(s.outbox) == len(kept) {
			return
		}
		slog.Info("voice outbox merged", "was", len(kept), "now", len(s.outbox))
		if err := s.keepOutbox(); err != nil {
			slog.Warn("voice outbox not written", "err", err)
		}
	}
}

// numbered stamps a change as it joins the outbox. Callers hold s.mu or own s outright.
func (s *Service) numbered(c change) change {
	s.queued++
	c.seq = s.queued
	return c
}

func (s *Service) keepOutbox() error {
	if s.o.Outbox == "" {
		return nil
	}
	raw, err := json.Marshal(s.outbox)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.o.Outbox), 0o755); err != nil {
		return fmt.Errorf("voice outbox: %w", err)
	}
	tmp := s.o.Outbox + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("voice outbox: %w", err)
	}
	return os.Rename(tmp, s.o.Outbox)
}

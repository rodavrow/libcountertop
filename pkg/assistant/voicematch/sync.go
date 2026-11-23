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
	"strconv"
	"time"

	"github.com/ygelfand/libcountertop/pkg/homeassistant"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

const (
	exchange     = "echolocal.voices"
	reply        = "{{ response | to_json }}"
	syncLimit    = 30 * time.Second
	batchBytes   = 32 << 10
	pageRestarts = 2
)

var errVoicesChanging = errors.New("voicematch: the household's voices kept changing during a sync")

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

	seq uint64
}

func (c change) voice() string {
	if c.Save != nil {
		return c.Save.ID
	}
	return c.Remove
}

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

	x := &exchanger{home: s.o.Home}
	sent := 0
	for {
		batch, raw, more, err := s.batch()
		if err != nil {
			return err
		}
		got, err := x.exchange(ctx, raw, 0)
		if err != nil {
			return err
		}
		if err := s.delivered(batch); err != nil {
			return err
		}
		sent += len(batch)
		if more {
			continue
		}
		profiles, err := x.rest(ctx, got)
		if err != nil {
			return err
		}
		if err := s.SetProfiles(profiles); err != nil {
			return err
		}
		slog.Info("voices synced", "sent", sent, "voices", len(profiles))
		return nil
	}
}

type answer struct {
	Profiles []Profile `json:"profiles"`
	Next     *int      `json:"next"`
	Revision int       `json:"revision"`
}

type exchanger struct {
	home    Asker
	unpaged bool
}

func (x *exchanger) rest(ctx context.Context, got answer) ([]Profile, error) {
	profiles := got.Profiles
	restarts := 0
	for got.Next != nil {
		next, err := x.exchange(ctx, []byte("[]"), *got.Next)
		if err != nil {
			return nil, err
		}
		if next.Revision != got.Revision {
			if restarts++; restarts > pageRestarts {
				return nil, errVoicesChanging
			}
			if next, err = x.exchange(ctx, []byte("[]"), 0); err != nil {
				return nil, err
			}
			profiles = nil
		}
		profiles = append(profiles, next.Profiles...)
		got = next
	}
	return profiles, nil
}

func (s *Service) batch() (batch []change, raw []byte, more bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw = []byte{'['}
	for _, c := range s.outbox {
		one, err := json.Marshal(c)
		if err != nil {
			return nil, nil, false, err
		}
		if len(batch) > 0 && len(raw)+1+len(one)+1 > batchBytes {
			return batch, append(raw, ']'), true, nil
		}
		if len(batch) > 0 {
			raw = append(raw, ',')
		}
		raw, batch = append(raw, one...), append(batch, c)
	}
	return batch, append(raw, ']'), false, nil
}

func (x *exchanger) exchange(ctx context.Context, changes []byte, page int) (answer, error) {
	data := map[string]string{"changes": string(changes)}
	if !x.unpaged {
		data["page"] = strconv.Itoa(page)
	}
	raw, err := x.home.Ask(ctx, exchange, data, nil, reply)
	var refused *homeassistant.Failed
	if err != nil && !x.unpaged && errors.As(err, &refused) {
		x.unpaged = true
		delete(data, "page")
		raw, err = x.home.Ask(ctx, exchange, data, nil, reply)
	}
	if err != nil {
		return answer{}, fmt.Errorf("voicematch: syncing voices: %w", err)
	}
	var got answer
	if err := json.Unmarshal(raw, &got); err != nil {
		return answer{}, fmt.Errorf("voicematch: the household's voices: %w", err)
	}
	return got, nil
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

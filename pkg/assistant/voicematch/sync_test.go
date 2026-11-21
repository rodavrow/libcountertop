package voicematch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type home struct {
	mu      sync.Mutex
	stored  map[string]Profile
	sent    [][]change
	sizes   []int
	offline bool
	refused int
}

func (h *home) online(after int) {
	for {
		h.mu.Lock()
		if h.refused >= after {
			h.offline = false
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
}

func (h *home) Ask(
	_ context.Context,
	action string,
	data, _ map[string]string,
	_ string,
) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.offline {
		h.refused++
		return nil, errors.New("not connected")
	}
	if action != exchange {
		return nil, errors.New("unknown action " + action)
	}
	if data["changes"] == "null" {
		return nil, errors.New("changes sent as null, want a list")
	}
	var changes []change
	if err := json.Unmarshal([]byte(data["changes"]), &changes); err != nil {
		return nil, err
	}
	h.sent = append(h.sent, changes)
	h.sizes = append(h.sizes, len(data["changes"]))
	for _, c := range changes {
		if c.Remove != "" {
			delete(h.stored, c.Remove)
		}
		if c.Save != nil {
			h.stored[c.Save.ID] = *c.Save
		}
	}
	var out struct {
		Profiles []Profile `json:"profiles"`
	}
	for _, p := range h.stored {
		out.Profiles = append(out.Profiles, p)
	}
	return json.Marshal(out)
}

func synced(t *testing.T, h *home) *Service {
	t.Helper()
	dir := t.TempDir()
	return settled(t, NewService(Options{
		ModelDir: dir,
		Profiles: filepath.Join(dir, "voices.json"),
		Outbox:   filepath.Join(dir, "outbox.json"),
		Home:     h,
	}))
}

func settled(t *testing.T, s *Service) *Service {
	t.Cleanup(s.syncs.Wait)
	return s
}

func ann() Profile {
	return Profile{ID: "a", Name: "Ann", Threshold: Default, Prints: [][]float32{{1, 0}}}
}

func TestChangesMadeOfflineAreSentOnceHomeAssistantAnswers(t *testing.T) {
	h := &home{stored: map[string]Profile{}, offline: true}
	s := synced(t, h)
	if err := s.Save(ann()); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background()); err == nil {
		t.Fatal("an offline sync should fail")
	}
	h.online(2)
	again := settled(t, NewService(s.o))
	if err := again.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.stored["a"]; !ok {
		t.Error("a voice saved while offline never reached Home Assistant, even after a restart")
	}
	if err := again.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if last := h.sent[len(h.sent)-1]; len(last) != 0 {
		t.Errorf("changes already delivered were sent again: %+v", last)
	}
}

func TestTheHouseholdListReplacesTheLocalOne(t *testing.T) {
	bo := Profile{ID: "b", Name: "Bo", Threshold: Default, Prints: [][]float32{{0, 1}}}
	h := &home{stored: map[string]Profile{"b": bo}}
	s := synced(t, h)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Profiles(); len(got) != 1 || got[0].ID != "b" {
		t.Errorf("profiles after sync %+v, want Bo from Home Assistant", got)
	}
}

func TestVoicesEnrolledBeforeSyncingAreSentUpFirst(t *testing.T) {
	dir := t.TempDir()
	o := Options{ModelDir: dir, Profiles: filepath.Join(dir, "voices.json")}
	if err := NewService(o).SetProfiles([]Profile{ann()}); err != nil {
		t.Fatal(err)
	}
	h := &home{stored: map[string]Profile{}}
	o.Outbox, o.Home = filepath.Join(dir, "outbox.json"), h
	s := settled(t, NewService(o))
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Profiles(); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("an older local voice was lost on the first sync: %+v", got)
	}
}

func TestARemovalTravelsToo(t *testing.T) {
	h := &home{stored: map[string]Profile{}}
	s := synced(t, h)
	_ = s.Save(ann())
	_ = s.Sync(context.Background())
	if err := s.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.stored["a"]; ok {
		t.Error("the removal never reached Home Assistant")
	}
}

func (s *Service) waiting() []change {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]change{}, s.outbox...)
}

// enrolled is a voice the size a device really holds: five prints of 192 numbers, about 10 KB.
func enrolled(id string) Profile {
	prints := make([][]float32, 5)
	for i := range prints {
		prints[i] = make([]float32, 192)
		for j := range prints[i] {
			prints[i][j] = float32(i*192+j) + 0.123456
		}
	}
	return Profile{ID: id, Name: id, Threshold: Default, Prints: prints}
}

func TestRegressionSavingOneVoiceRepeatedlyQueuesItOnce(t *testing.T) {
	h := &home{stored: map[string]Profile{}, offline: true}
	s := synced(t, h)
	p := ann()

	for i := range 19 {
		p.Threshold = Loosest + float64(i)*0.01
		if err := s.Save(p); err != nil {
			t.Fatal(err)
		}
	}

	got := s.waiting()
	if len(got) != 1 {
		t.Fatalf("%d changes queued for one voice, want 1", len(got))
	}
	if got[0].Save == nil || got[0].Save.Threshold != p.Threshold {
		t.Errorf("the queued change is %+v, want the last save", got[0])
	}
}

func TestARemovalReplacesAQueuedSaveOfTheSameVoice(t *testing.T) {
	h := &home{stored: map[string]Profile{}, offline: true}
	s := synced(t, h)
	if err := s.Save(ann()); err != nil {
		t.Fatal(err)
	}

	if err := s.Remove("a"); err != nil {
		t.Fatal(err)
	}

	got := s.waiting()
	if len(got) != 1 || got[0].Remove != "a" {
		t.Errorf("queued %+v, want only the removal", got)
	}
}

func TestRegressionABloatedOutboxIsMergedWhenLoaded(t *testing.T) {
	dir := t.TempDir()
	outbox := filepath.Join(dir, "outbox.json")
	p := ann()
	var bloated []change
	for i := range 19 {
		copied := p
		copied.Threshold = Loosest + float64(i)*0.01
		bloated = append(bloated, change{Save: &copied})
	}
	raw, err := json.Marshal(bloated)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outbox, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	s := settled(t, NewService(Options{
		ModelDir: dir,
		Profiles: filepath.Join(dir, "voices.json"),
		Outbox:   outbox,
		Home:     &home{stored: map[string]Profile{}, offline: true},
	}))

	got := s.waiting()
	if len(got) != 1 || got[0].Save == nil || got[0].Save.Threshold != bloated[18].Save.Threshold {
		t.Fatalf("loaded %d changes, want the last save alone", len(got))
	}
	kept, err := os.ReadFile(outbox)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) >= len(raw) {
		t.Errorf("the outbox file is still %d bytes after loading, was %d", len(kept), len(raw))
	}
}

func TestRegressionAnOutboxTooLargeForOneRequestIsSentInPieces(t *testing.T) {
	h := &home{stored: map[string]Profile{}, offline: true}
	s := synced(t, h)
	const voices = 12
	for i := range voices {
		if err := s.Save(enrolled(fmt.Sprintf("voice-%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	s.syncs.Wait()
	h.mu.Lock()
	h.offline = false
	h.mu.Unlock()

	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sizes) < 2 {
		t.Errorf("%d requests carried %d voices, want several", len(h.sizes), voices)
	}
	for _, size := range h.sizes {
		if size > batchBytes {
			t.Errorf("a request carried %d bytes of changes, at most %d", size, batchBytes)
		}
	}
	if len(h.stored) != voices {
		t.Errorf("Home Assistant holds %d voices, want %d", len(h.stored), voices)
	}
	if got := s.Profiles(); len(got) != voices {
		t.Errorf("the device holds %d voices after syncing, want %d", len(got), voices)
	}
	if left := s.waiting(); len(left) != 0 {
		t.Errorf("%d changes still queued after a full sync", len(left))
	}
}

func TestRegressionSavingAnUnchangedVoiceQueuesNothing(t *testing.T) {
	h := &home{stored: map[string]Profile{}}
	s := synced(t, h)
	if err := s.Save(ann()); err != nil {
		t.Fatal(err)
	}
	s.syncs.Wait()
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	before := len(h.sent)
	h.mu.Unlock()

	if err := s.Save(ann()); err != nil {
		t.Fatal(err)
	}
	s.syncs.Wait()

	if got := s.waiting(); len(got) != 0 {
		t.Errorf("an unchanged save queued %d changes", len(got))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sent) != before {
		t.Errorf("an unchanged save was sent to Home Assistant")
	}
}

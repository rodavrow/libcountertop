package youtube

import (
	"context"
	"testing"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/playback"
)

func ids(vs []Video) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.ID
	}
	return out
}

func TestUpNextIsTheRestOfTheListThenRelated(t *testing.T) {
	p := NewPlayer(t.Context(), PlayerOptions{})
	p.list = []Video{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	p.index = 1
	p.related = []Video{{ID: "a"}, {ID: "x"}, {ID: "c"}, {ID: "y"}}
	got := ids(p.Upcoming())
	want := []string{"c", "x", "y"}
	if len(got) != len(want) {
		t.Fatalf("up next %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("up next %v, want %v", got, want)
		}
	}
	if prev, next := p.around(); !prev || !next {
		t.Errorf("around %v %v", prev, next)
	}
}

func TestTheLastVideoWithNothingRelatedHasNoNext(t *testing.T) {
	p := NewPlayer(t.Context(), PlayerOptions{})
	p.list = []Video{{ID: "a"}}
	if prev, next := p.around(); prev || next {
		t.Errorf("around %v %v", prev, next)
	}
	if _, ok := p.Playing(); ok {
		t.Error("an idle player says it is playing")
	}
}

type changes struct {
	playback.Output
	n       int
	handed  []playback.Source
	stopped []playback.Source
}

func (c *changes) Changed(playback.Source)   { c.n++ }
func (c *changes) Handoff(s playback.Source) { c.handed = append(c.handed, s) }
func (c *changes) Stop(s playback.Source)    { c.stopped = append(c.stopped, s) }

func (c *changes) Position(playback.Source) (time.Duration, bool) { return 0, false }

func TestStoppingAHandedOffVideoClosesItsCard(t *testing.T) {
	out := &changes{}
	p := NewPlayer(t.Context(), PlayerOptions{Output: out})
	old := &source{p: p, Track: &Track{}}
	cur := &source{p: p, Track: &Track{}}
	p.cur = cur
	p.end(true)
	if len(out.handed) != 1 || out.handed[0] != cur {
		t.Fatalf("handed %v", out.handed)
	}
	p.stop(old)
	if len(out.stopped) != 0 {
		t.Fatalf("a stale video's stop reached the output: %v", out.stopped)
	}
	p.stop(cur)
	if len(out.stopped) != 1 || out.stopped[0] != cur {
		t.Errorf("stopping the handed-off video stopped %v", out.stopped)
	}
	if _, ok := p.Playing(); ok {
		t.Error("the player still plays after stopping")
	}
}

func TestAStreamFetchesMoreOnlyNearItsEnd(t *testing.T) {
	out := &changes{}
	p := NewPlayer(t.Context(), PlayerOptions{Output: out})
	s := &source{p: p}
	asked := 0
	p.cur, p.list = s, []Video{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	p.more = func(context.Context) []Video {
		asked++
		return []Video{{ID: "d"}, {ID: "e"}}
	}
	p.relate(t.Context(), s)
	if asked != 0 {
		t.Fatalf("fetched more %d times at the start of a long list", asked)
	}
	p.index = 2
	p.relate(t.Context(), s)
	if got := ids(p.list); asked != 1 || len(got) != 5 || got[4] != "e" || out.n != 1 {
		t.Errorf("after nearing the end: asked %d, list %v, changes %d", asked, got, out.n)
	}
}

func TestTheSelectedAccountIsTheProfile(t *testing.T) {
	raw := `{"contents":[{"accountSectionListRenderer":{"contents":[{"accountItemSectionRenderer":{"contents":[
		{"accountItem":{"accountName":{"simpleText":"Brand"},"accountPhoto":{"thumbnails":[{"url":"https://yt3.example/b"}]}}},
		{"accountItem":{"accountName":{"runs":[{"text":"Person"}]},"isSelected":true,
			"accountPhoto":{"thumbnails":[{"url":"https://yt3.example/s"},{"url":"https://yt3.example/l"}]}}}]}}]}}]}`
	p, err := parseProfile([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if p != (Profile{Name: "Person", Photo: "https://yt3.example/l"}) {
		t.Errorf("profile %+v", p)
	}
	if _, err := parseProfile([]byte(`{"contents":{}}`)); err == nil {
		t.Error("a reply with no account gave a profile")
	}
}

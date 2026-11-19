package youtubeapp

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/youtube"
	"github.com/ygelfand/libcountertop/pkg/runtime/safe"
)

const (
	askTime = 20 * time.Second
	stale   = 10 * time.Minute
)

type feedKind int

const (
	browseFeed feedKind = iota + 1
	searchFeed
	shortsFeed
)

type feedKey struct {
	account string
	kind    feedKind
	browse  string
	query   string
}

func browsing(account, browse string) feedKey {
	return feedKey{account: account, kind: browseFeed, browse: browse}
}

func searching(account, query string) feedKey {
	return feedKey{account: account, kind: searchFeed, query: query}
}

func shortsStream(account string) feedKey { return feedKey{account: account, kind: shortsFeed} }

func (k feedKey) first(ctx context.Context, s *youtube.Session) (youtube.Page, error) {
	switch k.kind {
	case searchFeed:
		return s.Search(ctx, k.query)
	case shortsFeed:
		return s.Shorts(ctx)
	}
	return s.Browse(ctx, k.browse)
}

func (k feedKey) next(ctx context.Context, s *youtube.Session, token string) (youtube.Page, error) {
	switch k.kind {
	case searchFeed:
		return s.SearchMore(ctx, token)
	case shortsFeed:
		return s.MoreShorts(ctx, token)
	}
	return s.More(ctx, token)
}

type feed struct {
	shelves []youtube.Shelf
	more    string
	loading bool
	loaded  bool
	failed  bool
	at      time.Time
}

type feeds struct {
	redraw func()

	mu    sync.Mutex
	byKey map[feedKey]*feed
}

func newFeeds(redraw func()) *feeds {
	return &feeds{redraw: redraw, byKey: map[feedKey]*feed{}}
}

func (f *feeds) get(k feedKey, s *youtube.Session) feed {
	f.mu.Lock()
	cur := f.byKey[k]
	due := cur == nil || !cur.loading && time.Since(cur.at) > stale
	if cur == nil {
		if k.kind == searchFeed {
			f.dropSearches(k.account)
		}
		cur = &feed{}
		f.byKey[k] = cur
	}
	if due {
		cur.loading = true
	}
	out := *cur
	f.mu.Unlock()
	if due {
		safe.Go("youtube feed", func() { f.load(k, s, "") })
	}
	return out
}

func (f *feeds) dropSearches(account string) {
	for k := range f.byKey {
		if k.account == account && k.kind == searchFeed {
			delete(f.byKey, k)
		}
	}
}

func (f *feeds) refetching(k feedKey) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.byKey[k]
	return cur != nil && cur.loading && cur.loaded && cur.at.IsZero()
}

func (f *feeds) more(k feedKey, s *youtube.Session, seen int) bool {
	token, ok := f.claim(k, seen)
	if !ok {
		return false
	}
	f.redraw()
	safe.Go("youtube feed more", func() { f.load(k, s, token) })
	return true
}

func (f *feeds) nextBatch(k feedKey, s *youtube.Session) []youtube.Video {
	f.mu.Lock()
	cur := f.byKey[k]
	seen := -1
	if cur != nil {
		seen = len(videos(cur.shelves))
	}
	f.mu.Unlock()
	token, ok := f.claim(k, seen)
	if !ok {
		return nil
	}
	f.redraw()
	f.load(k, s, token)
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur = f.byKey[k]; cur == nil {
		return nil
	}
	all := videos(cur.shelves)
	return slices.Clone(all[min(seen, len(all)):])
}

func (f *feeds) claim(k feedKey, seen int) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.byKey[k]
	if cur == nil || cur.loading || cur.more == "" || len(videos(cur.shelves)) != seen {
		return "", false
	}
	cur.loading = true
	return cur.more, true
}

func (f *feeds) moreShelf(k feedKey, s *youtube.Session, shelf, seen int) bool {
	f.mu.Lock()
	cur := f.byKey[k]
	if cur == nil || cur.loading || shelf < 0 || shelf >= len(cur.shelves) ||
		cur.shelves[shelf].More == "" || len(cur.shelves[shelf].Videos) != seen {
		f.mu.Unlock()
		return false
	}
	cur.loading = true
	token := cur.shelves[shelf].More
	f.mu.Unlock()
	f.redraw()
	safe.Go("youtube shelf more", func() { f.loadShelf(k, s, shelf, token) })
	return true
}

func (f *feeds) refresh(account string) {
	f.mu.Lock()
	for k, v := range f.byKey {
		if k.account == account && !v.loading {
			v.at = time.Time{}
		}
	}
	f.mu.Unlock()
	f.redraw()
}

func (f *feeds) forget(account string) {
	f.mu.Lock()
	for k := range f.byKey {
		if k.account == account {
			delete(f.byKey, k)
		}
	}
	f.mu.Unlock()
}

func (f *feeds) load(k feedKey, s *youtube.Session, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), askTime)
	defer cancel()
	var (
		p   youtube.Page
		err error
	)
	if token == "" {
		p, err = k.first(ctx, s)
	} else {
		p, err = k.next(ctx, s, token)
	}
	slog.Debug("youtube feed load", "kind", k.kind, "browse", k.browse,
		"next", token != "", "videos", len(videos(p.Shelves)), "err", err)
	f.mu.Lock()
	cur := f.byKey[k]
	if cur == nil {
		f.mu.Unlock()
		return
	}
	cur.loading = false
	switch {
	case err != nil:
		slog.Warn("youtube feed", "browse", k.browse, "err", err)
		cur.failed = !cur.loaded
		cur.at = time.Now()
		cur.more = ""
	case token == "":
		cur.shelves, cur.more, cur.loaded, cur.failed, cur.at = p.Shelves, p.More, true, false, time.Now()
	case len(videos(p.Shelves)) == 0:
		cur.more = ""
	default:
		cur.shelves = append(slices.Clone(cur.shelves), p.Shelves...)
		cur.more = p.More
	}
	f.mu.Unlock()
	f.redraw()
}

func (f *feeds) loadShelf(k feedKey, s *youtube.Session, shelf int, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), askTime)
	defer cancel()
	p, err := k.next(ctx, s, token)
	slog.Debug("youtube shelf load", "browse", k.browse, "shelf", shelf,
		"videos", len(videos(p.Shelves)), "err", err)
	f.mu.Lock()
	cur := f.byKey[k]
	if cur == nil {
		f.mu.Unlock()
		return
	}
	cur.loading = false
	if err != nil {
		slog.Warn("youtube shelf", "browse", k.browse, "err", err)
	}
	if shelf < len(cur.shelves) && cur.shelves[shelf].More == token {
		shelves := slices.Clone(cur.shelves)
		sh := shelves[shelf]
		got := videos(p.Shelves)
		sh.Videos = append(slices.Clone(sh.Videos), got...)
		sh.More = p.More
		if err != nil || len(got) == 0 {
			sh.More = ""
		}
		shelves[shelf] = sh
		cur.shelves = shelves
	}
	f.mu.Unlock()
	f.redraw()
}

func (f *feeds) related(ctx context.Context, s *youtube.Session, id string) []youtube.Video {
	p, err := s.Related(ctx, id)
	if err != nil {
		slog.Warn("youtube related", "video", id, "err", err)
		return nil
	}
	var out []youtube.Video
	for _, sh := range p.Shelves {
		for _, v := range sh.Videos {
			if v.Kind != youtube.Broadcast {
				out = append(out, v)
			}
		}
	}
	return out
}

func videos(shelves []youtube.Shelf) []youtube.Video {
	var out []youtube.Video
	for _, s := range shelves {
		out = append(out, s.Videos...)
	}
	return out
}

func shorts(shelves []youtube.Shelf) []youtube.Video {
	var out []youtube.Video
	seen := map[string]bool{}
	for _, v := range videos(shelves) {
		if v.Kind == youtube.Short && !seen[v.ID] {
			seen[v.ID] = true
			out = append(out, v)
		}
	}
	return out
}

func isShort(v youtube.Video) bool { return v.Kind == youtube.Short }

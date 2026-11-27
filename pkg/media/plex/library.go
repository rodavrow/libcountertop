package plex

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ygelfand/plexgo"
	"github.com/ygelfand/plexgo/models/components"
	"github.com/ygelfand/plexgo/models/operations"
)

const (
	hubCount   = 24
	searchSize = 30
	seeAll     = 200
	randomSort = "random"
	maxPicture = 16 << 20
)

type Library struct {
	ID      Identity
	HTTP    *http.Client
	Base    string
	Token   string
	Machine string
	Local   bool
}

type Section struct {
	Key   string
	Title string
	Type  string
}

type Hub struct {
	Title      string
	Key        string
	Identifier string
	Items      []Item
	More       bool
}

type Sort struct {
	Key     string
	Title   string
	Desc    string
	Default string
	Letters bool
}

type Letter struct {
	Title string
	Key   string
	Size  int
}

type Query struct {
	Sort      string
	Unwatched bool
	Genre     string
}

type Category struct {
	Title string
	Genre string
	Art   string
	Blend string
}

type Item struct {
	RatingKey string
	Key       string
	Type      string
	Title     string
	Show      string
	Season    string
	Summary   string
	Year      int
	Season0   int
	Episode   int
	Thumb     string
	Poster    string
	Art       string
	Duration  time.Duration
	Offset    time.Duration
	Viewed    bool
	Children  int
	Unwatched int
	Media     []Media
	Subtitles []Subtitle
	Provider  string
}

type Subtitle struct {
	ID       string
	Part     string
	Title    string
	Selected bool
}

type Media struct {
	Container string
	Video     string
	Audio     string
	Width     int
	Height    int
	Parts     []Part
}

type Part struct {
	Key       string
	Container string
}

func (i Item) Playable() bool {
	switch i.Type {
	case "movie", "episode", "clip", "track":
		return true
	}
	return false
}

func (i Item) Progress() float64 {
	if i.Duration <= 0 || i.Offset <= 0 {
		return 0
	}
	return min(float64(i.Offset)/float64(i.Duration), 1)
}

func (l *Library) api() *plexgo.PlexAPI { return l.ID.sdk(l.HTTP, l.Base, l.Token) }

func (l *Library) Sections(ctx context.Context) ([]Section, error) {
	res, err := l.api().Library.GetSections(ctx)
	if err != nil {
		return nil, quiet("libraries", err)
	}
	if err := status(res.StatusCode, "libraries"); err != nil {
		return nil, err
	}
	var out []Section
	if res.Object != nil && res.Object.MediaContainer != nil {
		for _, d := range res.Object.MediaContainer.Directory {
			out = append(
				out,
				Section{Key: deref(d.Key), Title: deref(d.Title), Type: string(d.Type)},
			)
		}
	}
	return out, nil
}

func (l *Library) Promoted(ctx context.Context) ([]Hub, error) {
	n := int64(hubCount)
	res, err := l.api().Hubs.GetPromotedHubs(ctx, operations.GetPromotedHubsRequest{Count: &n})
	if err != nil {
		return nil, quiet("home rows", err)
	}
	if err := status(res.StatusCode, "home rows"); err != nil {
		return nil, err
	}
	return hubs(res.MediaContainerWithHubs), nil
}

func (l *Library) Search(ctx context.Context, query string) ([]Hub, error) {
	n := int64(searchSize)
	res, err := l.api().Search.SearchHubs(
		ctx,
		operations.SearchHubsRequest{Query: query, Limit: &n},
	)
	if err != nil {
		return nil, quiet("search", err)
	}
	if err := status(res.StatusCode, "search"); err != nil {
		return nil, err
	}
	return hubs(res.MediaContainerWithHubs), nil
}

func (l *Library) Items(
	ctx context.Context,
	section string,
	q Query,
	start, size int,
) ([]Item, int, error) {
	id, err := sectionID(section)
	if err != nil {
		return nil, 0, err
	}
	from, many := int64(start), int64(size)
	req := operations.ListContentRequest{
		SectionID: id, XPlexContainerStart: &from, XPlexContainerSize: &many,
	}
	if q.Sort != "" {
		req.MediaQuery = &components.MediaQuery{Sort: &q.Sort}
	}
	if q.Unwatched {
		yes := components.BoolIntTrue
		req.Unwatched = &yes
	}
	if q.Genre != "" {
		req.Genre = &q.Genre
	}
	res, err := l.api().Content.ListContent(ctx, req)
	if err != nil {
		return nil, 0, quiet("library "+section, err)
	}
	if err := status(res.StatusCode, "library"); err != nil {
		return nil, 0, err
	}
	return listed(res.MediaContainerWithMetadata)
}

func (l *Library) SectionHubs(ctx context.Context, section string) ([]Hub, error) {
	id, err := sectionID(section)
	if err != nil {
		return nil, err
	}
	n := int64(hubCount)
	res, err := l.api().Hubs.GetSectionHubs(ctx, operations.GetSectionHubsRequest{
		SectionID: id, Count: &n,
	})
	if err != nil {
		return nil, quiet("library rows", err)
	}
	if err := status(res.StatusCode, "library rows"); err != nil {
		return nil, err
	}
	return hubs(res.MediaContainerWithHubs), nil
}

func (l *Library) HubItems(ctx context.Context, identifier string) ([]Item, error) {
	n := int64(seeAll)
	res, err := l.api().Hubs.GetHubItems(ctx, operations.GetHubItemsRequest{
		Identifier: []string{identifier}, Count: &n,
	})
	if err != nil {
		return nil, quiet("row", err)
	}
	if err := status(res.StatusCode, "row"); err != nil {
		return nil, err
	}
	var out []Item
	if res.Object != nil && res.Object.MediaContainer != nil {
		for _, m := range res.Object.MediaContainer.Metadata {
			out = append(out, item(m))
		}
	}
	return out, nil
}

func (l *Library) Sorts(ctx context.Context, section string) ([]Sort, error) {
	id, err := sectionID(section)
	if err != nil {
		return nil, err
	}
	res, err := l.api().Library.GetAvailableSorts(ctx, operations.GetAvailableSortsRequest{
		SectionID: id,
	})
	if err != nil {
		return nil, quiet("sorts", err)
	}
	if err := status(res.StatusCode, "sorts"); err != nil {
		return nil, err
	}
	var out []Sort
	if c := res.MediaContainerWithSorts; c != nil && c.MediaContainer != nil {
		for _, d := range c.MediaContainer.Directory {
			if deref(d.Key) == randomSort {
				continue
			}
			s := Sort{
				Key: deref(d.Key), Title: deref(d.Title), Desc: deref(d.DescKey),
				Letters: deref(d.FirstCharacterKey) != "",
			}
			if d.DefaultDirection != nil {
				s.Default = string(*d.DefaultDirection)
			}
			out = append(out, s)
		}
	}
	return out, nil
}

func (l *Library) Letters(ctx context.Context, section string) ([]Letter, error) {
	id, err := sectionID(section)
	if err != nil {
		return nil, err
	}
	res, err := l.api().Library.GetFirstCharacters(ctx, operations.GetFirstCharactersRequest{
		SectionID: id,
	})
	if err != nil {
		return nil, quiet("letters", err)
	}
	if err := status(res.StatusCode, "letters"); err != nil {
		return nil, err
	}
	var out []Letter
	if res.Object != nil && res.Object.MediaContainer != nil {
		for _, d := range res.Object.MediaContainer.Directory {
			out = append(out, Letter{Title: deref(d.Title), Key: deref(d.Key), Size: count(d.Size)})
		}
	}
	return out, nil
}

func (l *Library) Categories(ctx context.Context, section string) ([]Category, error) {
	id, err := sectionID(section)
	if err != nil {
		return nil, err
	}
	res, err := l.api().Content.GetCategories(ctx, operations.GetCategoriesRequest{SectionID: id})
	if err != nil {
		return nil, quiet("categories", err)
	}
	if err := status(res.StatusCode, "categories"); err != nil {
		return nil, err
	}
	var out []Category
	if c := res.MediaContainerWithArtwork; c != nil && c.MediaContainer != nil {
		for _, d := range c.MediaContainer.Directory {
			key, err := url.Parse(deref(d.Key))
			if err != nil {
				continue
			}
			genre := key.Query().Get("genre")
			if genre == "" {
				continue
			}
			cat := Category{Title: deref(d.Title), Genre: genre}
			if thumb, err := url.Parse(deref(d.Thumb)); err == nil {
				cat.Art, cat.Blend = thumb.Query().Get("url"), thumb.Query().Get("blendColor")
			}
			out = append(out, cat)
		}
	}
	return out, nil
}

func (l *Library) SetWatched(ctx context.Context, it Item, watched bool) error {
	if it.Provider == "" {
		return fmt.Errorf("plex: item %s: no provider", it.RatingKey)
	}
	key := it.RatingKey
	if watched {
		res, err := l.api().Timeline.MarkPlayed(ctx, operations.MarkPlayedRequest{
			Identifier: it.Provider, Key: &key,
		})
		if err != nil {
			return quiet("mark watched", err)
		}
		return status(res.StatusCode, "mark watched")
	}
	res, err := l.api().Timeline.Unscrobble(ctx, operations.UnscrobbleRequest{
		Identifier: it.Provider, Key: &key,
	})
	if err != nil {
		return quiet("mark unwatched", err)
	}
	return status(res.StatusCode, "mark unwatched")
}

func (l *Library) SetSubtitle(ctx context.Context, part, stream string) error {
	partID, err := strconv.ParseInt(part, 10, 64)
	if err != nil {
		return fmt.Errorf("plex: part %q: %w", part, err)
	}
	var streamID int64
	if stream != "" {
		if streamID, err = strconv.ParseInt(stream, 10, 64); err != nil {
			return fmt.Errorf("plex: subtitle %q: %w", stream, err)
		}
	}
	res, err := l.api().Library.SetStreamSelection(ctx, operations.SetStreamSelectionRequest{
		PartID: partID, SubtitleStreamID: &streamID,
	})
	if err != nil {
		return quiet("subtitles", err)
	}
	return status(res.StatusCode, "subtitles")
}

func sectionID(section string) (int64, error) {
	id, err := strconv.ParseInt(section, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("plex: library %q: %w", section, err)
	}
	return id, nil
}

func (l *Library) Children(ctx context.Context, ratingKey string) ([]Item, error) {
	id, err := strconv.ParseInt(ratingKey, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("plex: item %q: %w", ratingKey, err)
	}
	size := int64(500)
	res, err := l.api().Library.GetMetadataChildren(ctx, operations.GetMetadataChildrenRequest{
		ID: id, XPlexContainerSize: &size,
	})
	if err != nil {
		return nil, quiet("children of "+ratingKey, err)
	}
	if err := status(res.StatusCode, "children"); err != nil {
		return nil, err
	}
	items, _, err := listed(res.MediaContainerWithMetadata)
	return items, err
}

func (l *Library) Item(ctx context.Context, ratingKey string) (Item, error) {
	res, err := l.api().Content.GetMetadataItem(ctx, operations.GetMetadataItemRequest{
		Ids: []string{ratingKey},
	})
	if err != nil {
		return Item{}, quiet("item "+ratingKey, err)
	}
	if err := status(res.StatusCode, "item"); err != nil {
		return Item{}, err
	}
	items, _, err := listed(res.MediaContainerWithMetadata)
	if err != nil || len(items) == 0 {
		return Item{}, fmt.Errorf("plex: item %s: not found", ratingKey)
	}
	return items[0], nil
}

func (l *Library) Picture(path string, width, height int) string {
	if path == "" {
		return ""
	}
	v := url.Values{}
	v.Set("server", l.Machine)
	v.Set("url", path)
	v.Set("width", strconv.Itoa(width))
	v.Set("height", strconv.Itoa(height))
	return v.Encode()
}

func (l *Library) Tinted(path, blend string, width, height int) string {
	picture := l.Picture(path, width, height)
	if picture == "" || blend == "" {
		return picture
	}
	return picture + "&blend=" + url.QueryEscape(blend)
}

func (l *Library) fetch(ctx context.Context, picture string) ([]byte, error) {
	v, err := url.ParseQuery(picture)
	if err != nil || v.Get("server") != l.Machine || v.Get("url") == "" {
		return nil, ErrNoServer
	}
	width, _ := strconv.ParseInt(v.Get("width"), 10, 64)
	height, _ := strconv.ParseInt(v.Get("height"), 10, 64)
	path, jpeg, yes := v.Get("url"), operations.FormatJpeg, components.BoolIntTrue
	req := operations.TranscodeImageRequest{
		URL: &path, Format: &jpeg, Width: &width, Height: &height, MinSize: &yes, Upscale: &yes,
	}
	if blend := v.Get("blend"); blend != "" {
		req.BlendColor = &blend
	}
	res, err := l.api().Transcoder.TranscodeImage(ctx, req)
	if err != nil {
		return nil, quiet("picture", err)
	}
	body := cmp.Or(res.TwoHundredImageJpegResponseStream, res.TwoHundredImagePngResponseStream,
		res.TwoHundredImageXPortablePixmapResponseStream)
	if body == nil {
		return nil, fmt.Errorf("plex: picture: HTTP %d, no image", res.StatusCode)
	}
	defer body.Close()
	return io.ReadAll(io.LimitReader(body, maxPicture))
}

func hubs(c *components.MediaContainerWithHubs) []Hub {
	if c == nil || c.MediaContainer == nil {
		return nil
	}
	var out []Hub
	for _, h := range c.MediaContainer.Hub {
		hub := Hub{
			Title: deref(h.Title), Key: deref(h.Key), Identifier: deref(h.HubIdentifier),
			More: h.More != nil && *h.More,
		}
		for _, m := range h.Metadata {
			hub.Items = append(hub.Items, item(m))
		}
		if len(hub.Items) > 0 {
			out = append(out, hub)
		}
	}
	return out
}

func listed(c *components.MediaContainerWithMetadata) ([]Item, int, error) {
	if c == nil || c.MediaContainer == nil {
		return nil, 0, nil
	}
	mc := c.MediaContainer
	out := make([]Item, 0, len(mc.Metadata))
	for _, m := range mc.Metadata {
		it := item(m)
		it.Provider = deref(mc.Identifier)
		out = append(out, it)
	}
	total := len(out)
	if mc.TotalSize != nil {
		total = int(*mc.TotalSize)
	}
	return out, total, nil
}

func item(m components.Metadata) Item {
	it := Item{
		RatingKey: deref(m.RatingKey),
		Key:       m.Key,
		Type:      deref(m.Type),
		Title:     m.Title,
		Show:      deref(m.GrandparentTitle),
		Season:    deref(m.ParentTitle),
		Summary:   deref(m.Summary),
		Year:      count(m.Year),
		Season0:   count(m.ParentIndex),
		Episode:   count(m.Index),
		Thumb:     first(m.Thumb, m.ParentThumb, m.GrandparentThumb),
		Art:       first(m.Art, m.GrandparentArt),
		Duration:  time.Duration(derefInt(m.Duration)) * time.Millisecond,
		Offset:    time.Duration(derefInt(m.ViewOffset)) * time.Millisecond,
		Viewed:    derefInt(m.ViewCount) > 0,
		Children:  count(m.LeafCount),
	}
	it.Poster = it.Thumb
	if it.Type == "episode" {
		it.Poster = first(m.GrandparentThumb, m.ParentThumb, m.Thumb)
	}
	if m.LeafCount != nil {
		it.Unwatched = count(m.LeafCount) - count(m.ViewedLeafCount)
	}
	for _, md := range m.Media {
		media := Media{
			Container: deref(md.Container),
			Video:     deref(md.VideoCodec),
			Audio:     deref(md.AudioCodec),
			Width:     count(md.Width),
			Height:    count(md.Height),
		}
		for _, p := range md.Part {
			media.Parts = append(media.Parts, Part{Key: p.Key, Container: deref(p.Container)})
			if len(it.Media) > 0 || len(media.Parts) > 1 {
				continue
			}
			for _, s := range p.Stream {
				if s.StreamType != components.StreamTypeSubtitle {
					continue
				}
				it.Subtitles = append(it.Subtitles, Subtitle{
					ID:       strconv.FormatInt(int64(s.ID), 10),
					Part:     strconv.FormatInt(int64(p.ID), 10),
					Title:    s.DisplayTitle,
					Selected: s.Selected != nil && *s.Selected,
				})
			}
		}
		it.Media = append(it.Media, media)
	}
	return it
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt(n *int64) int64 {
	if n == nil {
		return 0
	}
	return *n
}

func count(n *int64) int { return int(derefInt(n)) }

func first(ss ...*string) string {
	for _, s := range ss {
		if s != nil && *s != "" {
			return *s
		}
	}
	return ""
}

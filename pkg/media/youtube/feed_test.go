package youtube

import (
	"strings"
	"testing"
	"time"
)

func tileJSON(id, title, channel, detail, overlay, kind string, watched string) string {
	overlays := `{"thumbnailOverlayTimeStatusRenderer":{"text":{"simpleText":"` + overlay + `"},"style":"DEFAULT"}}`
	if overlay == "LIVE" {
		overlays = `{"thumbnailOverlayTimeStatusRenderer":{"text":{"runs":[{"text":"LIVE"}]},"style":"LIVE"}}`
	}
	if watched != "" {
		overlays = `{"thumbnailOverlayResumePlaybackRenderer":{"percentDurationWatched":` + watched + `}},` + overlays
	}
	return `{"tileRenderer":{"contentType":"` + kind + `","contentId":"` + id + `",
	"header":{"tileHeaderRenderer":{"thumbnail":{"thumbnails":[
		{"url":"https://i.ytimg.com/vi/` + id + `/default.jpg"},
		{"url":"https://i.ytimg.com/vi/` + id + `/hqdefault.jpg"}]},
		"thumbnailOverlays":[` + overlays + `]}},
	"metadata":{"tileMetadataRenderer":{"title":{"simpleText":"` + title + `"},"lines":[
		{"lineRenderer":{"items":[{"lineItemRenderer":{"text":{"runs":[{"text":"` + channel + `"}]}}}]}},
		{"lineRenderer":{"items":[
			{"lineItemRenderer":{"text":{"simpleText":"` + detail + `"}}},
			{"lineItemRenderer":{"text":{"simpleText":" "}}},
			{"lineItemRenderer":{"text":{"simpleText":"•"}}},
			{"lineItemRenderer":{"text":{"simpleText":"2 days ago"}}}]}}]}},
	"onSelectCommand":{"` + endpointFor(kind) + `":{"videoId":"` + id + `"}}}}`
}

func endpointFor(kind string) string {
	if kind == "TILE_CONTENT_TYPE_SHORTS" {
		return "reelWatchEndpoint"
	}
	return "watchEndpoint"
}

func TestAShortInAVideoTileIsStillAShort(t *testing.T) {
	short := strings.Replace(
		tileJSON("aaaaaaaaaaa", "Reel", "Chan", "9K views", "0:30", "TILE_CONTENT_TYPE_VIDEO", ""),
		`"watchEndpoint"`, `"reelWatchEndpoint"`, 1,
	)
	p, err := parsePage([]byte(`{"contents":{"gridRenderer":{"items":[` + short + `]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Shelves) != 1 || p.Shelves[0].Videos[0].Kind != Short ||
		p.Shelves[0].Videos[0].ID != "aaaaaaaaaaa" {
		t.Errorf("shelves %+v", p.Shelves)
	}
}

func shelfJSON(title, cont string, tiles ...string) string {
	items := ""
	for i, t := range tiles {
		if i > 0 {
			items += ","
		}
		items += t
	}
	return `{"shelfRenderer":{"headerRenderer":{"shelfHeaderRenderer":{"avatarLockup":{"avatarLockupRenderer":{"title":{"runs":[{"text":"` + title + `"}]}}}}},
	"content":{"horizontalListRenderer":{"items":[` + items + `],
	"continuations":[{"nextContinuationData":{"continuation":"` + cont + `"}}]}}}}`
}

func TestHomeComesBackAsTitledShelves(t *testing.T) {
	raw := `{"contents":{"tvBrowseRenderer":{"content":{"tvSurfaceContentRenderer":{"content":{"sectionListRenderer":{
	"contents":[` +
		shelfJSON(
			"Recommended",
			"row1",
			tileJSON(
				"aaaaaaaaaaa",
				"First",
				"Chan A",
				"1.5M views",
				"8:43",
				"TILE_CONTENT_TYPE_VIDEO",
				"",
			),
			tileJSON(
				"bbbbbbbbbbb",
				"Second",
				"Chan B",
				"622 watching",
				"LIVE",
				"TILE_CONTENT_TYPE_VIDEO",
				"",
			),
		) + "," +
		shelfJSON(
			"Shorts",
			"row2",
			tileJSON(
				"ccccccccccc",
				"Third",
				"Chan C",
				"9K views",
				"0:41",
				"TILE_CONTENT_TYPE_SHORTS",
				"",
			),
		) + `],
	"continuations":[{"nextContinuationData":{"continuation":"page2"}}]}}}}}}}`
	p, err := parsePage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if p.More != "page2" {
		t.Errorf("more is %q, want the page's own continuation, not a row's", p.More)
	}
	if len(p.Shelves) != 2 || p.Shelves[0].Title != "Recommended" ||
		p.Shelves[1].Title != "Shorts" {
		t.Fatalf("shelves %+v", p.Shelves)
	}
	first := p.Shelves[0].Videos[0]
	want := Video{
		ID:        "aaaaaaaaaaa",
		Title:     "First",
		Channel:   "Chan A",
		Detail:    "1.5M views • 2 days ago",
		Length:    8*time.Minute + 43*time.Second,
		Thumbnail: Picture{URL: "https://i.ytimg.com/vi/aaaaaaaaaaa/hqdefault.jpg"},
		Kind:      Clip,
	}
	if first != want {
		t.Errorf("first video\n got %+v\nwant %+v", first, want)
	}
	if k := p.Shelves[0].Videos[1].Kind; k != Broadcast {
		t.Errorf("a LIVE tile is kind %d", k)
	}
	if k := p.Shelves[1].Videos[0].Kind; k != Short {
		t.Errorf("a shorts tile is kind %d", k)
	}
	if p.Shelves[0].More != "row1" || p.Shelves[1].More != "row2" {
		t.Errorf("row continuations %q %q", p.Shelves[0].More, p.Shelves[1].More)
	}
}

func TestARowContinuationComesBackAsOneShelf(t *testing.T) {
	raw := `{"continuationContents":{"horizontalListContinuation":{"items":[` +
		tileJSON(
			"ccccccccccc",
			"Next",
			"Chan C",
			"9K views",
			"0:41",
			"TILE_CONTENT_TYPE_SHORTS",
			"",
		) +
		`],"continuations":[{"nextContinuationData":{"continuation":"row3"}}]}}}`
	p, err := parsePage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Shelves) != 1 || p.Shelves[0].Videos[0].Kind != Short || p.More != "row3" {
		t.Errorf("page %+v", p)
	}
}

func TestHistoryComesBackAsOneShelfWithProgress(t *testing.T) {
	raw := `{"contents":{"tvBrowseRenderer":{"content":{"tvSurfaceContentRenderer":{"content":{"gridRenderer":{
	"items":[` +
		tileJSON(
			"aaaaaaaaaaa",
			"Watched",
			"Chan A",
			"364K views",
			"1:11",
			"TILE_CONTENT_TYPE_VIDEO",
			"100",
		) + "," +
		tileJSON(
			"bbbbbbbbbbb",
			"Started",
			"Chan B",
			"398K views",
			"1:02:01",
			"TILE_CONTENT_TYPE_VIDEO",
			"10",
		) + `],
	"continuations":[{"nextContinuationData":{"continuation":"older"}}]}}}}}}}`
	raw = strings.Replace(
		raw,
		`{"videoId":"bbbbbbbbbbb"}`,
		`{"videoId":"bbbbbbbbbbb","startTimeSeconds":372}`,
		1,
	)
	p, err := parsePage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Shelves) != 1 || p.Shelves[0].Title != "" || len(p.Shelves[0].Videos) != 2 {
		t.Fatalf("shelves %+v", p.Shelves)
	}
	v := p.Shelves[0].Videos[1]
	if v.Watched != 10 || v.Length != time.Hour+2*time.Minute+time.Second ||
		v.Resume != 372*time.Second {
		t.Errorf("second video %+v", v)
	}
	if first := p.Shelves[0].Videos[0]; first.Resume != 0 {
		t.Errorf("first video resumes at %v, want the top", first.Resume)
	}
	if p.More != "older" {
		t.Errorf("more %q", p.More)
	}
}

func TestAPlaylistComesBackFromItsRightColumn(t *testing.T) {
	raw := `{"contents":{"tvBrowseRenderer":{"content":{"tvSurfaceContentRenderer":{"content":{"twoColumnRenderer":{
	"leftColumn":{"entityMetadataRenderer":{"title":{"simpleText":"Watch later"},"buttons":[{"buttonRenderer":{
		"navigationEndpoint":{"watchEndpoint":{"videoId":"aaaaaaaaaaa","playlistId":"WL"}}}}]}},
	"rightColumn":{"playlistVideoListRenderer":{"contents":[` +
		tileJSON(
			"aaaaaaaaaaa",
			"One",
			"Chan A",
			"26K views",
			"1:33:32",
			"TILE_CONTENT_TYPE_VIDEO",
			"",
		) + "," +
		tileJSON(
			"bbbbbbbbbbb",
			"Two",
			"Chan B",
			"47K views",
			"3:23:50",
			"TILE_CONTENT_TYPE_VIDEO",
			"",
		) + `]}}}}}}}}}`
	p, err := parsePage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Shelves) != 1 || len(p.Shelves[0].Videos) != 2 ||
		p.Shelves[0].Videos[1].ID != "bbbbbbbbbbb" {
		t.Fatalf("shelves %+v", p.Shelves)
	}
	if p.More != "" {
		t.Errorf("more %q from a page with no continuation", p.More)
	}
}

func TestATileWithoutAVideoIsLeftOut(t *testing.T) {
	raw := `{"contents":{"gridRenderer":{"items":[{"tileRenderer":{"contentType":"TILE_CONTENT_TYPE_CHANNEL",
	"metadata":{"tileMetadataRenderer":{"title":{"simpleText":"A channel"}}},
	"onSelectCommand":{"browseEndpoint":{"browseId":"UC1"}}}}]}}}`
	p, err := parsePage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Shelves) != 0 {
		t.Errorf("shelves %+v", p.Shelves)
	}
}

func TestAShortTakesItsUprightThumbnail(t *testing.T) {
	list := []Picture{
		{URL: "tall-small", Width: 203, Height: 360},
		{URL: "cropped", Width: 203, Height: 305},
		{URL: "tall-big", Width: 405, Height: 720},
		{URL: "cropped-big", Width: 405, Height: 608},
	}
	if got := pickThumbnail(list, true); got != list[2] {
		t.Errorf("a short took %+v", got)
	}
	if got := pickThumbnail(list, false); got != list[3] {
		t.Errorf("a video took %+v", got)
	}
	if got := pickThumbnail(list[1:2], true); got != list[1] {
		t.Errorf("with nothing upright, a short took %+v", got)
	}
}

func lockupJSON(id, kind, title, badge, style string) string {
	return `{"lockupViewModel":{"contentId":"` + id + `","contentType":"` + kind + `",` +
		`"contentImage":{"thumbnailViewModel":{"image":{"sources":[` +
		`{"url":"https://i.example/` + id + `.jpg","width":320,"height":180}]},` +
		`"overlays":[{"thumbnailBottomOverlayViewModel":{"badges":[{"thumbnailBadgeViewModel":` +
		`{"text":"` + badge + `","badgeStyle":"` + style + `"}}]}}]}},` +
		`"metadata":{"lockupMetadataViewModel":{"title":{"content":"` + title + `"},` +
		`"metadata":{"contentMetadataViewModel":{"delimiter":" • ","metadataRows":[` +
		`{"metadataParts":[{"text":{"content":"Chan"}}]},` +
		`{"metadataParts":[{"text":{"content":"2M views"}},{"text":{"content":"1 day ago"}}]}]}}}}}}`
}

func TestSearchLockupsComeBackAsVideos(t *testing.T) {
	plain := "THUMBNAIL_OVERLAY_BADGE_STYLE_DEFAULT"
	raw := `{"contents":{"sectionListRenderer":{"contents":[{"shelfRenderer":{"content":` +
		`{"horizontalListRenderer":{"items":[` +
		lockupJSON("vvvvvvvvvvv", "LOCKUP_CONTENT_TYPE_VIDEO", "A video", "1:04:42", plain) + `,` +
		lockupJSON("sssssssssss", "LOCKUP_CONTENT_TYPE_SHORT", "A short", "", plain) + `,` +
		lockupJSON("lllllllllll", "LOCKUP_CONTENT_TYPE_VIDEO", "Live", "LIVE",
			"THUMBNAIL_OVERLAY_BADGE_STYLE_LIVE") + `,` +
		lockupJSON("PLaaaaaaaaa", "LOCKUP_CONTENT_TYPE_PLAYLIST", "A list", "", plain) +
		`]}}}}],"continuations":[{"nextContinuationData":{"continuation":"next1"}}]}}}`
	p, err := parsePage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	got := videos(p)
	if len(got) != 3 || p.More != "next1" {
		t.Fatalf("page %+v", p)
	}
	want := Video{
		ID:        "vvvvvvvvvvv",
		Title:     "A video",
		Channel:   "Chan",
		Detail:    "2M views • 1 day ago",
		Length:    time.Hour + 4*time.Minute + 42*time.Second,
		Thumbnail: Picture{URL: "https://i.example/vvvvvvvvvvv.jpg", Width: 320, Height: 180},
		Kind:      Clip,
	}
	if got[0] != want {
		t.Errorf("video %+v", got[0])
	}
	if got[1].Kind != Short || got[2].Kind != Broadcast {
		t.Errorf("kinds %v %v", got[1].Kind, got[2].Kind)
	}
}

func videos(p Page) []Video {
	var out []Video
	for _, s := range p.Shelves {
		out = append(out, s.Videos...)
	}
	return out
}

func TestClockLengths(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"0:41":    41 * time.Second,
		"8:43":    8*time.Minute + 43*time.Second,
		"3:23:50": 3*time.Hour + 23*time.Minute + 50*time.Second,
		"LIVE":    0,
		"":        0,
	} {
		if got := clock(in); got != want {
			t.Errorf("%q is %v, want %v", in, got, want)
		}
	}
}

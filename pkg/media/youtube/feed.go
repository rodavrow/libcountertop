package youtube

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Kind int

const (
	Clip Kind = iota + 1
	Broadcast
	Short
)

type Video struct {
	ID        string
	Title     string
	Channel   string
	Detail    string
	Length    time.Duration
	Thumbnail Picture
	Kind      Kind
	Watched   int
	Resume    time.Duration
}

type Picture struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type Shelf struct {
	Title  string
	Videos []Video
	More   string
}

type Page struct {
	Shelves []Shelf
	More    string
}

type text struct {
	SimpleText string `json:"simpleText"`
	Runs       []struct {
		Text string `json:"text"`
	} `json:"runs"`
}

func (t text) String() string {
	if t.SimpleText != "" {
		return t.SimpleText
	}
	var b strings.Builder
	for _, r := range t.Runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

type tile struct {
	ContentType string `json:"contentType"`
	Header      struct {
		Renderer struct {
			Thumbnail struct {
				Thumbnails []Picture `json:"thumbnails"`
			} `json:"thumbnail"`
			Overlays []struct {
				Time *struct {
					Text  text   `json:"text"`
					Style string `json:"style"`
				} `json:"thumbnailOverlayTimeStatusRenderer"`
				Resume *struct {
					Percent int `json:"percentDurationWatched"`
				} `json:"thumbnailOverlayResumePlaybackRenderer"`
			} `json:"thumbnailOverlays"`
		} `json:"tileHeaderRenderer"`
	} `json:"header"`
	Metadata struct {
		Renderer struct {
			Title text `json:"title"`
			Lines []struct {
				Line struct {
					Items []struct {
						Item struct {
							Text text `json:"text"`
						} `json:"lineItemRenderer"`
					} `json:"items"`
				} `json:"lineRenderer"`
			} `json:"lines"`
		} `json:"tileMetadataRenderer"`
	} `json:"metadata"`
	OnSelect struct {
		Watch *endpoint `json:"watchEndpoint"`
		Reel  *endpoint `json:"reelWatchEndpoint"`
	} `json:"onSelectCommand"`
}

type lockup struct {
	ContentID   string `json:"contentId"`
	ContentType string `json:"contentType"`
	Image       struct {
		Thumbnail struct {
			Image struct {
				Sources []Picture `json:"sources"`
			} `json:"image"`
			Overlays []struct {
				Bottom struct {
					Badges []struct {
						Badge struct {
							Text  string `json:"text"`
							Style string `json:"badgeStyle"`
						} `json:"thumbnailBadgeViewModel"`
					} `json:"badges"`
				} `json:"thumbnailBottomOverlayViewModel"`
			} `json:"overlays"`
		} `json:"thumbnailViewModel"`
	} `json:"contentImage"`
	Metadata struct {
		Model struct {
			Title struct {
				Content string `json:"content"`
			} `json:"title"`
			Metadata struct {
				Content struct {
					Rows []struct {
						Parts []struct {
							Text struct {
								Content string `json:"content"`
							} `json:"text"`
						} `json:"metadataParts"`
					} `json:"metadataRows"`
					Delimiter string `json:"delimiter"`
				} `json:"contentMetadataViewModel"`
			} `json:"metadata"`
		} `json:"lockupMetadataViewModel"`
	} `json:"metadata"`
}

func (l lockup) video() (Video, bool) {
	v := Video{ID: l.ContentID, Kind: Clip}
	switch l.ContentType {
	case "LOCKUP_CONTENT_TYPE_VIDEO", "LOCKUP_CONTENT_TYPE_MUSIC":
	case "LOCKUP_CONTENT_TYPE_SHORT":
		v.Kind = Short
	default:
		return Video{}, false
	}
	if v.ID == "" {
		return Video{}, false
	}
	md := l.Metadata.Model
	v.Title = md.Title.Content
	sep := cmp.Or(md.Metadata.Content.Delimiter, " • ")
	for i, row := range md.Metadata.Content.Rows {
		var parts []string
		for _, p := range row.Parts {
			if s := strings.TrimSpace(p.Text.Content); s != "" {
				parts = append(parts, s)
			}
		}
		switch i {
		case 0:
			v.Channel = strings.Join(parts, sep)
		case 1:
			v.Detail = strings.Join(parts, sep)
		}
	}
	th := l.Image.Thumbnail
	v.Thumbnail = pickThumbnail(th.Image.Sources, v.Kind == Short)
	for _, o := range th.Overlays {
		for _, b := range o.Bottom.Badges {
			switch {
			case b.Badge.Style == "THUMBNAIL_OVERLAY_BADGE_STYLE_LIVE":
				v.Kind = Broadcast
			case v.Length == 0:
				v.Length = clock(b.Badge.Text)
			}
		}
	}
	return v, true
}

type endpoint struct {
	VideoID string `json:"videoId"`
	Start   int64  `json:"startTimeSeconds"`
}

func pickThumbnail(list []Picture, upright bool) Picture {
	best := -1
	for i, t := range list {
		if !upright || t.Height*9 < t.Width*15 {
			continue
		}
		if best < 0 || t.Width*t.Height > list[best].Width*list[best].Height {
			best = i
		}
	}
	if best < 0 {
		if len(list) == 0 {
			return Picture{}
		}
		return list[len(list)-1]
	}
	return list[best]
}

type shelfHeader struct {
	Renderer struct {
		Title  text `json:"title"`
		Avatar struct {
			Renderer struct {
				Title text `json:"title"`
			} `json:"avatarLockupRenderer"`
		} `json:"avatarLockup"`
	} `json:"shelfHeaderRenderer"`
}

func (h shelfHeader) title() string {
	if t := h.Renderer.Avatar.Renderer.Title.String(); t != "" {
		return t
	}
	return h.Renderer.Title.String()
}

func parsePage(raw []byte) (Page, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return Page{}, fmt.Errorf("youtube: browse: %w", err)
	}
	var w walker
	for _, key := range []string{"contents", "continuationContents"} {
		if n, ok := root[key]; ok {
			w.walk(n)
		}
	}
	w.close()
	return Page{Shelves: w.shelves, More: w.more}, nil
}

type walker struct {
	shelves []Shelf
	open    *Shelf
	titled  bool
	more    string
}

func (w *walker) walk(n any) {
	switch v := n.(type) {
	case []any:
		for _, c := range v {
			w.walk(c)
		}
	case map[string]any:
		for _, k := range sortedKeys(v) {
			c := v[k]
			switch k {
			case "shelfRenderer":
				w.shelf(c)
			case "tileRenderer":
				w.tile(c)
			case "lockupViewModel":
				w.lockup(c)
			case "nextContinuationData":
				m, ok := c.(map[string]any)
				switch {
				case !ok:
				case w.titled && w.open.More == "":
					w.open.More, _ = m["continuation"].(string)
				case !w.titled && w.more == "":
					w.more, _ = m["continuation"].(string)
				}
			default:
				w.walk(c)
			}
		}
	}
}

func (w *walker) shelf(n any) {
	m, ok := n.(map[string]any)
	if !ok {
		return
	}
	var h shelfHeader
	decode(m["headerRenderer"], &h)
	w.close()
	w.open, w.titled = &Shelf{Title: h.title()}, true
	w.walk(m["content"])
	w.close()
}

func (w *walker) tile(n any) {
	var t tile
	if !decode(n, &t) {
		return
	}
	if v, ok := t.video(); ok {
		w.add(v)
	}
}

func (w *walker) lockup(n any) {
	var l lockup
	if !decode(n, &l) {
		return
	}
	if v, ok := l.video(); ok {
		w.add(v)
	}
}

func (w *walker) add(v Video) {
	if w.open == nil {
		w.open = &Shelf{}
	}
	w.open.Videos = append(w.open.Videos, v)
}

func (w *walker) close() {
	if w.open != nil && len(w.open.Videos) > 0 {
		w.shelves = append(w.shelves, *w.open)
	}
	w.open, w.titled = nil, false
}

func (t tile) video() (Video, bool) {
	md := t.Metadata.Renderer
	v := Video{Title: md.Title.String(), Kind: Clip}
	switch {
	case t.OnSelect.Watch != nil:
		v.ID = t.OnSelect.Watch.VideoID
		v.Resume = time.Duration(t.OnSelect.Watch.Start) * time.Second
	case t.OnSelect.Reel != nil:
		v.ID, v.Kind = t.OnSelect.Reel.VideoID, Short
	}
	if v.ID == "" {
		return Video{}, false
	}
	if t.ContentType == "TILE_CONTENT_TYPE_SHORTS" {
		v.Kind = Short
	}
	for i, l := range md.Lines {
		var parts []string
		for _, it := range l.Line.Items {
			if s := strings.TrimSpace(it.Item.Text.String()); s != "" {
				parts = append(parts, s)
			}
		}
		line := strings.Join(parts, " ")
		switch i {
		case 0:
			v.Channel = line
		case 1:
			v.Detail = line
		}
	}
	h := t.Header.Renderer
	v.Thumbnail = pickThumbnail(h.Thumbnail.Thumbnails, v.Kind == Short)
	for _, o := range h.Overlays {
		switch {
		case o.Time != nil && o.Time.Style == "LIVE":
			v.Kind = Broadcast
		case o.Time != nil:
			v.Length = clock(o.Time.Text.String())
		case o.Resume != nil:
			v.Watched = o.Resume.Percent
		}
	}
	return v, true
}

type accountItem struct {
	Name     text `json:"accountName"`
	Selected bool `json:"isSelected"`
	Photo    struct {
		Thumbnails []struct {
			URL string `json:"url"`
		} `json:"thumbnails"`
	} `json:"accountPhoto"`
}

func parseProfile(raw []byte) (Profile, error) {
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return Profile{}, fmt.Errorf("youtube: account: %w", err)
	}
	var found []accountItem
	collect(root, &found)
	if len(found) == 0 {
		return Profile{}, errors.New("youtube: account: no account in the reply")
	}
	pick := found[0]
	for _, a := range found {
		if a.Selected {
			pick = a
			break
		}
	}
	p := Profile{Name: pick.Name.String()}
	if n := len(pick.Photo.Thumbnails); n > 0 {
		p.Photo = pick.Photo.Thumbnails[n-1].URL
	}
	return p, nil
}

func collect(n any, found *[]accountItem) {
	switch v := n.(type) {
	case []any:
		for _, c := range v {
			collect(c, found)
		}
	case map[string]any:
		if _, ok := v["accountName"]; ok {
			var a accountItem
			if decode(v, &a) {
				*found = append(*found, a)
			}
			return
		}
		for _, k := range sortedKeys(v) {
			collect(v[k], found)
		}
	}
}

func clock(s string) time.Duration {
	var d time.Duration
	for part := range strings.SplitSeq(s, ":") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		d = d*60 + time.Duration(n)
	}
	return d * time.Second
}

func decode(n any, out any) bool {
	raw, err := json.Marshal(n)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, out) == nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

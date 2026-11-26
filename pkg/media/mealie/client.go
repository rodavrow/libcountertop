package mealie

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/fetch"
)

const (
	askTime    = 20 * time.Second
	maxReply   = 8 << 20
	maxPicture = 16 << 20
)

var (
	ErrToken    = errors.New("mealie: the API token was refused")
	ErrNoServer = errors.New("mealie: no server set")
	ErrNotFound = errors.New("mealie: not found")
)

type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func NewClient(base, token string) *Client {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base != "" && !strings.Contains(base, "://") {
		base = "http://" + base
	}
	return &Client{Base: base, Token: strings.TrimSpace(token), HTTP: fetch.Client(askTime)}
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	FullName string `json:"fullName"`
}

type Tag struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Summary struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Image       *string  `json:"image"`
	Servings    float64  `json:"recipeServings"`
	Yield       string   `json:"recipeYield"`
	TotalTime   string   `json:"totalTime"`
	PrepTime    string   `json:"prepTime"`
	CookTime    string   `json:"performTime"`
	TotalSecs   *int     `json:"totalTimeSeconds"`
	PrepSecs    *int     `json:"prepTimeSeconds"`
	CookSecs    *int     `json:"performTimeSeconds"`
	Rating      *float64 `json:"rating"`
	Categories  []Tag    `json:"recipeCategory"`
	Tags        []Tag    `json:"tags"`
}

func (s Summary) HasImage() bool { return s.Image != nil && *s.Image != "" }

func (s Summary) Total() time.Duration {
	for _, n := range []*int{s.TotalSecs, s.CookSecs} {
		if n != nil && *n > 0 {
			return time.Duration(*n) * time.Second
		}
	}
	if s.PrepSecs != nil && s.CookSecs != nil {
		return time.Duration(*s.PrepSecs+*s.CookSecs) * time.Second
	}
	return 0
}

type Unit struct {
	Name           string `json:"name"`
	PluralName     string `json:"pluralName"`
	Abbreviation   string `json:"abbreviation"`
	PluralAbbrev   string `json:"pluralAbbreviation"`
	UseAbbrevation bool   `json:"useAbbreviation"`
	Fraction       *bool  `json:"fraction"`
}

type Food struct {
	Name       string `json:"name"`
	PluralName string `json:"pluralName"`
}

type Ingredient struct {
	Quantity  *float64 `json:"quantity"`
	Unit      *Unit    `json:"unit"`
	Food      *Food    `json:"food"`
	Note      string   `json:"note"`
	Display   string   `json:"display"`
	Title     string   `json:"title"`
	Original  string   `json:"originalText"`
	Reference string   `json:"referenceId"`
}

type Reference struct {
	ID string `json:"referenceId"`
}

type Step struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Summary     string      `json:"summary"`
	Text        string      `json:"text"`
	Ingredients []Reference `json:"ingredientReferences"`
	Seconds     *int        `json:"durationSeconds"`
}

func (s Step) Duration() time.Duration {
	if s.Seconds == nil || *s.Seconds <= 0 {
		return 0
	}
	return time.Duration(*s.Seconds) * time.Second
}

type Recipe struct {
	Summary
	Ingredients []Ingredient `json:"recipeIngredient"`
	Steps       []Step       `json:"recipeInstructions"`
	Source      string       `json:"orgURL"`
}

type Page struct {
	Page  int       `json:"page"`
	Pages int       `json:"total_pages"`
	Total int       `json:"total"`
	Items []Summary `json:"items"`
}

type Query struct {
	Search string
	IDs    []string
	Page   int
	Per    int
}

func (c *Client) Self(ctx context.Context) (User, error) {
	var out User
	return out, c.call(ctx, http.MethodGet, "/api/users/self", nil, &out)
}

func (c *Client) Recipes(ctx context.Context, q Query) (Page, error) {
	v := url.Values{}
	v.Set("page", strconv.Itoa(max(q.Page, 1)))
	v.Set("perPage", strconv.Itoa(max(q.Per, 1)))
	if s := strings.TrimSpace(q.Search); s != "" {
		v.Set("search", s)
	} else {
		v.Set("orderBy", "name")
		v.Set("orderDirection", "asc")
	}
	if q.IDs != nil {
		v.Set("queryFilter", "id IN ["+strings.Join(q.IDs, ", ")+"]")
	}
	var out Page
	return out, c.call(ctx, http.MethodGet, "/api/recipes?"+v.Encode(), nil, &out)
}

func (c *Client) Recipe(ctx context.Context, slug string) (Recipe, error) {
	var out Recipe
	return out, c.call(ctx, http.MethodGet, "/api/recipes/"+url.PathEscape(slug), nil, &out)
}

func (c *Client) Favorites(ctx context.Context) ([]string, error) {
	var out struct {
		Ratings []struct {
			Recipe   string `json:"recipeId"`
			Favorite bool   `json:"isFavorite"`
		} `json:"ratings"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/users/self/favorites", nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Ratings))
	for _, r := range out.Ratings {
		if r.Favorite {
			ids = append(ids, r.Recipe)
		}
	}
	return ids, nil
}

func (c *Client) SetFavorite(ctx context.Context, user, slug string, on bool) error {
	method := http.MethodDelete
	if on {
		method = http.MethodPost
	}
	path := "/api/users/" + url.PathEscape(user) + "/favorites/" + url.PathEscape(slug)
	return c.call(ctx, method, path, nil, nil)
}

const (
	Small    = "min-original.webp"
	Original = "original.webp"
)

func (c *Client) PictureURL(id, size string) string {
	return c.Base + "/api/media/recipes/" + url.PathEscape(id) + "/images/" + size
}

func (c *Client) Picture(ctx context.Context, address string) ([]byte, error) {
	if !strings.HasPrefix(address, c.Base+"/") {
		return nil, fmt.Errorf("mealie: %q is not on the server", address)
	}
	req, err := c.request(ctx, http.MethodGet, strings.TrimPrefix(address, c.Base), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := status(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxPicture))
}

func (c *Client) request(
	ctx context.Context,
	method, path string,
	body any,
) (*http.Request, error) {
	if c.Base == "" {
		return nil, ErrNoServer
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := status(resp); err != nil {
		return err
	}
	if out == nil {
		_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxReply))
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxReply)).Decode(out)
}

func status(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrToken
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode >= 300:
		return fmt.Errorf("mealie: %s", resp.Status)
	}
	return nil
}

package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	tvClientName    = "TVHTML5"
	tvClientVersion = "7.20260311.12.00"
	refreshEarly    = time.Minute
)

const (
	Home          = "FEwhat_to_watch"
	History       = "FEhistory"
	WatchLater    = "VLWL"
	Subscriptions = "FEsubscriptions"
)

type Account struct {
	Client  Client `json:"client"`
	Refresh string `json:"refresh"`
}

type Session struct {
	http    *http.Client
	account Account
	visitor *Visitor

	mu    sync.Mutex
	token Token
}

func NewSession(hc *http.Client, a Account) *Session {
	return &Session{http: hc, account: a}
}

func Anonymous(hc *http.Client, v *Visitor) *Session {
	return &Session{http: hc, visitor: v}
}

func SignedIn(hc *http.Client, c Client, t Token) *Session {
	return &Session{http: hc, account: Account{Client: c, Refresh: t.Refresh}, token: t}
}

func (s *Session) Account() Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account
}

type Profile struct {
	Name  string
	Photo string
}

func (s *Session) Profile(ctx context.Context) (Profile, error) {
	raw, err := s.post(ctx, "account/accounts_list", map[string]any{})
	if err != nil {
		return Profile{}, err
	}
	return parseProfile(raw)
}

func (s *Session) Related(ctx context.Context, id string) (Page, error) {
	return s.parsed(ctx, "next", map[string]any{"videoId": id})
}

func (s *Session) Browse(ctx context.Context, id string) (Page, error) {
	return s.parsed(ctx, "browse", map[string]any{"browseId": id})
}

func (s *Session) More(ctx context.Context, token string) (Page, error) {
	return s.parsed(ctx, "browse", map[string]any{"continuation": token})
}

func (s *Session) Search(ctx context.Context, query string) (Page, error) {
	return s.parsed(ctx, "search", map[string]any{"query": query})
}

func (s *Session) SearchMore(ctx context.Context, token string) (Page, error) {
	return s.parsed(ctx, "search", map[string]any{"continuation": token})
}

func (s *Session) parsed(ctx context.Context, endpoint string, body map[string]any) (Page, error) {
	raw, err := s.post(ctx, endpoint, body)
	if err != nil {
		return Page{}, err
	}
	return parsePage(raw)
}

func (s *Session) post(ctx context.Context, endpoint string, body map[string]any) ([]byte, error) {
	var access, visitor string
	if s.visitor != nil {
		visitor = s.visitor.ID()
	} else {
		var err error
		if access, err = s.access(ctx); err != nil {
			return nil, err
		}
	}
	client := map[string]string{"clientName": tvClientName, "clientVersion": tvClientVersion}
	if visitor != "" {
		client["visitorData"] = visitor
	}
	body["context"] = map[string]any{"client": client}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		base+"/youtubei/v1/"+endpoint+"?prettyPrint=false",
		bytes.NewReader(raw),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", cobalt)
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	if visitor != "" {
		req.Header.Set("X-Goog-Visitor-Id", visitor)
	}
	status, got, err := send(s.http, req)
	if err != nil {
		return nil, fmt.Errorf("youtube: %s: %w", endpoint, err)
	}
	if status/100 != 2 {
		return nil, fmt.Errorf("youtube: %s: %d", endpoint, status)
	}
	if s.visitor != nil {
		s.visitor.learn(got)
	}
	return got, nil
}

func (s *Session) access(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token.Access != "" && time.Until(s.token.Expiry) > refreshEarly {
		return s.token.Access, nil
	}
	t, err := Refresh(ctx, s.http, s.account.Client, s.account.Refresh)
	if err != nil {
		return "", err
	}
	s.token, s.account.Refresh = t, t.Refresh
	return t.Access, nil
}

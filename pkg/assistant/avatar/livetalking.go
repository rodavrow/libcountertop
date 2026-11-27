// Package avatar shows a wake word's LiveTalking avatar in place of the assistant's look and has it
// speak the reply, lip-synced, instead of the speaker playing it.
package avatar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path"
)

// maxAnswer bounds a LiveTalking JSON reply; an SDP answer is a few kilobytes.
const maxAnswer = 1 << 20

var errRefused = errors.New("avatar: LiveTalking refused")

// server is one LiveTalking instance, reached at base (http(s)://host[:port][/path]).
type server struct {
	client *http.Client
	base   string
}

type offerRequest struct {
	SDP    string `json:"sdp"`
	Type   string `json:"type"`
	Avatar string `json:"avatar,omitempty"`
}

type offerAnswer struct {
	SDP       string          `json:"sdp"`
	Type      string          `json:"type"`
	SessionID json.RawMessage `json:"sessionid"`
}

type session struct {
	SessionID string `json:"sessionid"`
}

type result struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
	// ReplyID names the reply Home Assistant streams into the session, from when it opens until
	// its last frame has played; empty for none, as on a LiveTalking that does not stream.
	ReplyID string `json:"reply_id"`
	// Holding says the streamed reply is open but paused at the source, as for a tool call.
	Holding bool `json:"holding"`
}

// offer opens a render session for a recv-only offer and returns the answer and the session id.
func (s server) offer(ctx context.Context, sdp, avatar string) (answer, id string, err error) {
	var got offerAnswer
	if err := s.postJSON(ctx, "offer", offerRequest{SDP: sdp, Type: "offer", Avatar: avatar}, &got); err != nil {
		return "", "", err
	}
	id, err = sessionID(got.SessionID)
	if err != nil {
		return "", "", err
	}
	if got.SDP == "" || got.Type != "answer" {
		return "", "", fmt.Errorf("%w: /offer answered type %q with %d bytes of SDP", errRefused, got.Type, len(got.SDP))
	}
	return got.SDP, id, nil
}

// sessionID accepts the id as a JSON string or number; LiveTalking versions differ.
func sessionID(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil && text != "" {
		return text, nil
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil && number != "" {
		return number.String(), nil
	}
	return "", fmt.Errorf("%w: /offer gave no session id", errRefused)
}

// speak has the session say clip, sent as is; LiveTalking takes MP3, WAV, FLAC and OGG.
func (s server) speak(ctx context.Context, id string, clip []byte, name string) error {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("sessionid", id); err != nil {
		return err
	}
	part, err := form.CreateFormFile("file", path.Base(name))
	if err != nil {
		return err
	}
	if _, err := part.Write(clip); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/humanaudio", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	var r result
	if err := s.do(req, &r); err != nil {
		return err
	}
	return r.ok("/humanaudio")
}

func (s server) interrupt(ctx context.Context, id string) error {
	var r result
	if err := s.postJSON(ctx, "interrupt_talk", session{SessionID: id}, &r); err != nil {
		return err
	}
	return r.ok("/interrupt_talk")
}

// speaking reports whether the session is still rendering speech. It turns false before the last
// of the audio arrives, so it is never the only sign a reply ended.
func (s server) speaking(ctx context.Context, id string) (bool, error) {
	st, err := s.status(ctx, id)
	return st.on, err
}

// state is what /is_speaking says about a session.
type state struct {
	// on is whether it is rendering speech.
	on bool
	// reply is the streamed reply open on it, if any.
	reply string
	// holding is whether that reply is paused at its source. It can be while the last of the
	// audio before the pause still renders, so on may still be true.
	holding bool
}

func (s server) status(ctx context.Context, id string) (state, error) {
	var r result
	if err := s.postJSON(ctx, "is_speaking", session{SessionID: id}, &r); err != nil {
		return state{}, err
	}
	if err := r.ok("/is_speaking"); err != nil {
		return state{}, err
	}
	var on bool
	if err := json.Unmarshal(r.Data, &on); err != nil {
		return state{}, fmt.Errorf("%w: /is_speaking data %s", errRefused, r.Data)
	}
	return state{on: on, reply: r.ReplyID, holding: r.Holding}, nil
}

func (r result) ok(endpoint string) error {
	if r.Code != 0 {
		return fmt.Errorf("%w: %s code %d %s", errRefused, endpoint, r.Code, r.Msg)
	}
	return nil
}

func (s server) postJSON(ctx context.Context, endpoint string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/"+endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return s.do(req, out)
}

func (s server) do(req *http.Request, out any) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s %s", errRefused, req.URL.Path, resp.Status)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: %s answered %d bytes that are not JSON", errRefused, req.URL.Path, len(body))
	}
	return nil
}

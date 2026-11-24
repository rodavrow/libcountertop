package avatar

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T, h http.HandlerFunc) server {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return server{client: ts.Client(), base: ts.URL + "/lt"}
}

func TestAnOfferSendsTheAvatarAndReturnsTheSession(t *testing.T) {
	var got offerRequest
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lt/offer" {
			t.Errorf("posted to %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"sdp":"v=0 answer","type":"answer","sessionid":123456}`)
	})

	answer, id, err := s.offer(context.Background(), "v=0 offer", "sylvia")

	if err != nil || answer != "v=0 answer" || id != "123456" {
		t.Fatalf("answer %q, id %q, err %v", answer, id, err)
	}
	if got.SDP != "v=0 offer" || got.Type != "offer" || got.Avatar != "sylvia" {
		t.Errorf("sent %+v", got)
	}
}

func TestAnOfferWithoutAnAvatarLeavesTheFieldOut(t *testing.T) {
	var raw map[string]any
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		_, _ = io.WriteString(w, `{"sdp":"x","type":"answer","sessionid":"abc"}`)
	})

	if _, id, err := s.offer(context.Background(), "v=0", ""); err != nil || id != "abc" {
		t.Fatalf("id %q, err %v", id, err)
	}
	if _, ok := raw["avatar"]; ok {
		t.Errorf("sent avatar in %v", raw)
	}
}

func TestAnOfferWithoutASessionIsRefused(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"sdp":"x","type":"answer"}`)
	})

	if _, _, err := s.offer(context.Background(), "v=0", ""); !errors.Is(err, errRefused) {
		t.Fatalf("err %v, want refused", err)
	}
}

func TestSpeakingUploadsTheClipAsIs(t *testing.T) {
	clip := []byte("ID3 fake mp3")
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lt/humanaudio" {
			t.Errorf("posted to %s", r.URL.Path)
		}
		if got := r.FormValue("sessionid"); got != "42" {
			t.Errorf("sessionid %q", got)
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(f)
		if string(body) != string(clip) || h.Filename != "reply.mp3" {
			t.Errorf("file %q named %q", body, h.Filename)
		}
		_, _ = io.WriteString(w, `{"code":0}`)
	})

	if err := s.speak(context.Background(), "42", clip, "/api/tts_proxy/reply.mp3"); err != nil {
		t.Fatal(err)
	}
}

func TestANonZeroCodeIsAnError(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":-1,"msg":"no session"}`)
	})

	if err := s.interrupt(context.Background(), "42"); !errors.Is(err, errRefused) {
		t.Fatalf("err %v, want refused", err)
	}
}

func TestSpeakingReadsTheFlag(t *testing.T) {
	for _, c := range []struct {
		body string
		want bool
	}{
		{`{"code":0,"msg":"ok","data":true}`, true},
		{`{"code":0,"msg":"ok","data":false}`, false},
	} {
		s := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, c.body) })
		got, err := s.speaking(context.Background(), "42")
		if err != nil || got != c.want {
			t.Errorf("%s: %v, %v", c.body, got, err)
		}
	}
}

func TestAnHTTPErrorIsRefused(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusBadGateway) })

	if _, err := s.speaking(context.Background(), "42"); !errors.Is(err, errRefused) {
		t.Fatalf("err %v, want refused", err)
	}
}

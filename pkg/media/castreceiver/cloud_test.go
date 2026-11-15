package castreceiver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ygelfand/libcountertop/pkg/media/cast"
	"github.com/ygelfand/libcountertop/pkg/media/playback"
	"github.com/ygelfand/libcountertop/pkg/settings/schema"
)

func TestByDefaultTheReceiverAsksGoogleLikeAStockDevice(t *testing.T) {
	r := NewReceiver(ReceiverOptions{
		Settings: &memory{c: schema.DefaultCast()},
		Server:   Config{Volume: silent{}},
	})
	if !r.server.cloud() {
		t.Error("a receiver with default settings refuses Google lookups")
	}
}

func TestTheCallersCloudSwitchReachesTheServer(t *testing.T) {
	r := NewReceiver(ReceiverOptions{
		Settings: &memory{c: schema.DefaultCast()},
		Server:   Config{Volume: silent{}, Cloud: func() bool { return false }},
	})
	if r.server.cloud() {
		t.Error("the receiver dropped the caller's Cloud switch")
	}
}

func TestTheRevocationListIsNotFetchedWithTheCloudOff(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("revoked"))
	}))
	t.Cleanup(s.Close)
	k := testKeys(t)
	k.crlURL = s.URL
	k.cloud = func() bool { return false }

	if wait := k.revocation(t.Context()); wait != retry {
		t.Errorf("the next look is in %v, want %v", wait, retry)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the revocation list was fetched %d times with the cloud off", n)
	}
	if k.CRL() != nil {
		t.Error("a revocation list is held with the cloud off")
	}
}

func TestTheRevocationListIsFetchedOnceTheCloudIsOn(t *testing.T) {
	on := false
	k := testKeys(t)
	k.crlURL = serve(t, []byte("revoked"))
	k.cloud = func() bool { return on }

	k.revocation(t.Context())
	on = true
	if wait := k.revocation(t.Context()); wait != crlEvery {
		t.Errorf("the next look is in %v, want %v", wait, crlEvery)
	}
	if got := string(k.CRL()); got != "revoked" {
		t.Errorf("the revocation list is %q", got)
	}
}

type refuseTransport struct{ t *testing.T }

func (r refuseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("a request went out with Google lookups off: %s", req.URL)
	return nil, errors.New("no network in this test")
}

func TestTheServerDescribesNothingWithTheCloudOff(t *testing.T) {
	s := New(Config{Cloud: func() bool { return false }, Volume: silent{}})
	client := &http.Client{Transport: refuseTransport{t}}
	if _, err := s.describe(client, "CC1AD845"); !errors.Is(err, cast.ErrCloudOff) {
		t.Errorf("describe = %v, want cast.ErrCloudOff", err)
	}
}

func TestTheProtocolEnvCarriesTheCloudSwitch(t *testing.T) {
	s := New(Config{
		Cloud:  func() bool { return false },
		Volume: silent{},
		Output: func(func()) playback.Output { return nil },
	})
	if s.env("Kitchen", cast.Device{}).CloudAllowed() {
		t.Error("the protocols' Env allows Google lookups the server does not")
	}
}

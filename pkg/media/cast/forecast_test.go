package cast

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

type forecastKeys struct {
	deviceKey *rsa.PrivateKey
	deviceDER []byte
	icaDER    []byte
	notBefore time.Time
	notAfter  time.Time
	singleICA bool
}

func newForecastKeys(t *testing.T, singleICA bool) forecastKeys {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "forecast device"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(48 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ica := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "forecast ica"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(72 * time.Hour),
	}
	icaDER, err := x509.CreateCertificate(rand.Reader, ica, ica, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return forecastKeys{
		deviceKey: key,
		deviceDER: der,
		icaDER:    icaDER,
		notBefore: tmpl.NotBefore,
		notAfter:  tmpl.NotAfter,
		singleICA: singleICA,
	}
}

func (k forecastKeys) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		var req ForecastRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		public, err := base64.StdEncoding.DecodeString(req.Public)
		if err != nil {
			t.Fatal(err)
		}
		var nonce []byte
		if req.Nonce != "" {
			if nonce, err = base64.StdEncoding.DecodeString(req.Nonce); err != nil {
				t.Fatal(err)
			}
		}

		reply := ForecastReply{
			Device:    base64.StdEncoding.EncodeToString(k.deviceDER),
			Public:    req.Public,
			NotBefore: k.notBefore.Unix(),
			NotAfter:  k.notAfter.Unix(),
			Now:       time.Now().Unix(),
		}
		if k.singleICA {
			reply.Intermediate = base64.StdEncoding.EncodeToString(k.icaDER)
		} else {
			reply.Intermediates = []string{base64.StdEncoding.EncodeToString(k.icaDER)}
		}
		for _, name := range req.Hashes {
			h := SHA1
			if name == "sha256" {
				h = SHA256
			}
			sig, err := SignForecast(k.deviceKey, h, nonce, public)
			if err != nil {
				t.Fatal(err)
			}
			enc := base64.StdEncoding.EncodeToString(sig)
			if h == SHA256 {
				reply.SHA256 = enc
			} else {
				reply.SHA1 = enc
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}
}

func peerCert(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "forecast peer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(48 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, der
}

func TestForecastRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name      string
		nonce     []byte
		hashes    []Hash
		singleICA bool
	}{
		{"nonce both hashes array ica", []byte("a-session-nonce"), []Hash{SHA1, SHA256}, false},
		{"empty nonce sha256 single ica", nil, []Hash{SHA256}, true},
		{"nonce sha256 only", []byte{0x00, 0x01, 0x02, 0xff}, []Hash{SHA256}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oracle := newForecastKeys(t, tc.singleICA)
			srv := httptest.NewServer(oracle.handler(t))
			defer srv.Close()

			peerKey, peerDER := peerCert(t)
			f := Forecast{URL: srv.URL, Client: srv.Client()}
			creds, now, err := f.Sign(context.Background(), peerDER, tc.nonce, tc.hashes, peerKey)
			if err != nil {
				t.Fatal(err)
			}

			if now.IsZero() {
				t.Error("oracle clock is zero")
			}
			if !creds.Remote {
				t.Error("credentials not marked remote")
			}
			if len(creds.Intermediates) != 1 {
				t.Fatalf("intermediates = %d, want 1", len(creds.Intermediates))
			}
			if creds.Peer.PrivateKey != peerKey {
				t.Error("peer private key not carried through")
			}
			for _, h := range tc.hashes {
				if err := VerifyForecast(
					creds.Device,
					tc.nonce,
					peerDER,
					creds.Signature(h),
					h,
				); err != nil {
					t.Errorf("%s signature does not verify: %v", h, err)
				}
			}
			if !slices.Contains(tc.hashes, SHA1) && len(creds.SHA1) != 0 {
				t.Error("sha1 returned though not requested")
			}
		})
	}
}

func TestForecastCertOnlyMatchesDeviceAuthShape(t *testing.T) {
	oracle := newForecastKeys(t, false)
	_, peerDER := peerCert(t)

	sig, err := SignForecast(oracle.deviceKey, SHA256, nil, peerDER)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyForecast(oracle.deviceDER, nil, peerDER, sig, SHA256); err != nil {
		t.Fatalf("cert-only signature does not verify: %v", err)
	}
	if err := VerifyForecast(oracle.deviceDER, []byte("x"), peerDER, sig, SHA256); err == nil {
		t.Fatal("a nonce was accepted against a cert-only signature")
	}
}

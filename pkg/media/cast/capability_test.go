package cast

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

func certWithPolicies(t *testing.T, oids ...string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	for _, s := range oids {
		oid, err := x509.ParseOID(s)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.Policies = append(tmpl.Policies, oid)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestAudioOnly(t *testing.T) {
	audio := certWithPolicies(t, audioOnlyPolicy)
	plain := certWithPolicies(t)

	cases := []struct {
		name string
		c    *Credentials
		want bool
	}{
		{"nil", nil, false},
		{"leaf audio-only", &Credentials{Device: audio}, true},
		{"leaf unrestricted", &Credentials{Device: plain}, false},
		{
			"intermediate audio-only",
			&Credentials{Device: plain, Intermediates: [][]byte{audio}},
			true,
		},
		{"all unrestricted", &Credentials{Device: plain, Intermediates: [][]byte{plain}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AudioOnly(c.c); got != c.want {
				t.Errorf("AudioOnly = %v, want %v", got, c.want)
			}
		})
	}
}

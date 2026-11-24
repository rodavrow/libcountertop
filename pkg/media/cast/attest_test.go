package cast

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestAttestHeaders(t *testing.T) {
	sign := func(_ context.Context, input string) (string, error) {
		return base64.RawURLEncoding.EncodeToString([]byte("sig:" + input)), nil
	}
	in := AttestInput{
		DeviceID:      "0123456789abcdef0123456789abcdef",
		AppID:         "CC32E753",
		SessionID:     "SESSION-7",
		Prod:          "chromecast",
		PrivateData:   `{"isOriginalChromecast":false}`,
		ClaimTemplate: `{"prod":"${CAST-PROD}","iss":"${CAST-APP-DEVICE-ID} ","jti":"${CAST-SESSION-ID}"}`,
		Creds: &Credentials{
			Device:        []byte("device-der"),
			Intermediates: [][]byte{[]byte("ica0"), []byte("ica1")},
		},
	}
	h, err := AttestHeaders(context.Background(), in, sign)
	if err != nil {
		t.Fatal(err)
	}

	appDeviceID := AppDeviceID("CC32E753", in.DeviceID)
	cases := map[string]string{
		"CAST-APP-ID":        "CC32E753",
		"CAST-SESSION-ID":    "SESSION-7",
		"CAST-PROD":          "chromecast",
		"CAST-APP-DEVICE-ID": appDeviceID,
		"CAST-CERT":          base64.StdEncoding.EncodeToString([]byte("device-der")),
		"CAST-CERT-CHAIN":    certChain([][]byte{[]byte("ica0"), []byte("ica1")}),
		"CAST-PRIVATE-DATA":  `{"isOriginalChromecast":false}`,
	}
	for k, want := range cases {
		if h[k] != want {
			t.Errorf("%s = %q, want %q", k, h[k], want)
		}
	}

	if parts := strings.Split(h["CAST-JWT"], "."); len(parts) != 3 {
		t.Errorf("CAST-JWT has %d segments, want 3", len(parts))
	}
}

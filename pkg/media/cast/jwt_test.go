package cast

import (
	"context"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

var uuidShape = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

func TestAppDeviceID(t *testing.T) {
	a := AppDeviceID("CC32E753", "0123456789abcdef0123456789abcdef")
	if !uuidShape.MatchString(a) {
		t.Fatalf("app device id %q is not a v5 UUID", a)
	}
	if AppDeviceID("CC32E753", "0123456789abcdef0123456789abcdef") != a {
		t.Error("app device id is not deterministic")
	}
	if AppDeviceID("CAD366A1", "0123456789abcdef0123456789abcdef") == a {
		t.Error("a different app yielded the same device id")
	}
	if AppDeviceID("CC32E753", "ffffffffffffffffffffffffffffffff") == a {
		t.Error("a different device yielded the same app device id")
	}
}

func TestRenderClaims(t *testing.T) {
	got := RenderClaims(
		`{"prod":"${CAST-PROD}","iss":"${CAST-APP-DEVICE-ID} ","jti":"${CAST-SESSION-ID}"}`,
		"chromecast", "11111111-1111-5111-8111-111111111111", "SESSION-7")
	want := `{"prod":"chromecast","iss":"11111111-1111-5111-8111-111111111111 ","jti":"SESSION-7"}`
	if got != want {
		t.Errorf("render = %s\n   want = %s", got, want)
	}
	got = RenderClaims(
		`{"iss":"${CAST-APP-DEVICE-ID} ","prod":"${CAST-PROD}","jti":"${CAST-SESSION-ID}"}`,
		"chromecast", "aaaa", "S2")
	if want := `{"iss":"aaaa ","prod":"chromecast","jti":"S2"}`; got != want {
		t.Errorf("render = %s\n   want = %s", got, want)
	}
}

func TestCastJWT(t *testing.T) {
	sign := func(_ context.Context, input string) (string, error) {
		return base64.RawURLEncoding.EncodeToString([]byte("sig:" + input)), nil
	}
	payload := `{"prod":"chromecast","iss":"x ","jti":"SESSION-7"}`
	tok, err := CastJWT(context.Background(), payload, sign)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3", len(parts))
	}
	header, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if string(header) != `{"alg":"RS256","typ":"JWT"}` {
		t.Errorf("header = %s", header)
	}
	if body, _ := base64.RawURLEncoding.DecodeString(parts[1]); string(body) != payload {
		t.Errorf("payload = %s, want %s", body, payload)
	}
	wantSig := base64.RawURLEncoding.EncodeToString([]byte("sig:" + parts[0] + "." + parts[1]))
	if parts[2] != wantSig {
		t.Errorf("signature segment = %s, want %s", parts[2], wantSig)
	}
}

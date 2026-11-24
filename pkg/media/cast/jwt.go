package cast

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"
)

var appDeviceNamespace = [16]byte{
	0x6b, 0xa7, 0xca, 0x57, 0x0d, 0x11, 0x5e, 0x41,
	0x9b, 0x32, 0x8f, 0x1c, 0x4d, 0x72, 0xe6, 0x03,
}

func AppDeviceID(appID, deviceID string) string {
	h := sha1.New()
	h.Write(appDeviceNamespace[:])
	h.Write([]byte(appID + ":" + deviceID))
	sum := h.Sum(nil)[:16]
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func RenderClaims(template, prod, appDeviceID, sessionID string) string {
	return strings.NewReplacer(
		"${CAST-PROD}", prod,
		"${CAST-APP-DEVICE-ID}", appDeviceID,
		"${CAST-SESSION-ID}", sessionID,
	).Replace(template)
}

type Signer func(ctx context.Context, input string) (string, error)

func CastJWT(ctx context.Context, payload string, sign Signer) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	input := header + "." + base64.RawURLEncoding.EncodeToString([]byte(payload))
	signature, err := sign(ctx, input)
	if err != nil {
		return "", err
	}
	return input + "." + signature, nil
}

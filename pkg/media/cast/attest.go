package cast

import (
	"context"
	"encoding/base64"
	"strings"
)

type AttestInput struct {
	DeviceID      string
	AppID         string
	SessionID     string
	Prod          string
	PrivateData   string
	ClaimTemplate string
	Creds         *Credentials
}

func AttestHeaders(ctx context.Context, in AttestInput, sign Signer) (map[string]string, error) {
	appDeviceID := AppDeviceID(in.AppID, in.DeviceID)
	jwt, err := CastJWT(
		ctx,
		RenderClaims(in.ClaimTemplate, in.Prod, appDeviceID, in.SessionID),
		sign,
	)
	if err != nil {
		return nil, err
	}
	h := map[string]string{
		"CAST-JWT":           jwt,
		"CAST-APP-ID":        in.AppID,
		"CAST-SESSION-ID":    in.SessionID,
		"CAST-APP-DEVICE-ID": appDeviceID,
		"CAST-PROD":          in.Prod,
		"CAST-CERT":          base64.StdEncoding.EncodeToString(in.Creds.Device),
	}
	if chain := certChain(in.Creds.Intermediates); chain != "" {
		h["CAST-CERT-CHAIN"] = chain
	}
	if in.PrivateData != "" {
		h["CAST-PRIVATE-DATA"] = in.PrivateData
	}
	return h, nil
}

func certChain(intermediates [][]byte) string {
	parts := make([]string, len(intermediates))
	for i, der := range intermediates {
		parts[i] = base64.StdEncoding.EncodeToString(der)
	}
	return strings.Join(parts, ";")
}

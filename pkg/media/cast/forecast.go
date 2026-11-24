package cast

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const forecastMost = 1 << 20

type ForecastRequest struct {
	Public string   `json:"public"`
	Nonce  string   `json:"nonce,omitempty"`
	Hashes []string `json:"hashes"`
}

type ForecastReply struct {
	Device        string   `json:"device"`
	Intermediate  string   `json:"intermediate,omitempty"`
	Intermediates []string `json:"intermediates,omitempty"`
	Public        string   `json:"public"`
	SHA1          string   `json:"sha1,omitempty"`
	SHA256        string   `json:"sha256,omitempty"`
	NotBefore     int64    `json:"notBefore"`
	NotAfter      int64    `json:"notAfter"`
	Now           int64    `json:"now"`
}

func (h Hash) cryptoHash() crypto.Hash {
	if h == SHA256 {
		return crypto.SHA256
	}
	return crypto.SHA1
}

func forecastDigest(h Hash, nonce, peer []byte) []byte {
	sum := h.cryptoHash().New()
	sum.Write(nonce)
	sum.Write(peer)
	return sum.Sum(nil)
}

func SignForecast(key *rsa.PrivateKey, h Hash, nonce, peer []byte) ([]byte, error) {
	return rsa.SignPKCS1v15(rand.Reader, key, h.cryptoHash(), forecastDigest(h, nonce, peer))
}

func VerifyForecast(deviceDER, nonce, peer, signature []byte, h Hash) error {
	cert, err := x509.ParseCertificate(deviceDER)
	if err != nil {
		return fmt.Errorf("cast: forecast device certificate: %w", err)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("cast: forecast device certificate is not RSA")
	}
	return rsa.VerifyPKCS1v15(pub, h.cryptoHash(), forecastDigest(h, nonce, peer), signature)
}

func ForecastBody(public, nonce []byte, hashes []Hash) ([]byte, error) {
	names := make([]string, len(hashes))
	for i, h := range hashes {
		names[i] = h.String()
	}
	req := ForecastRequest{Public: base64.StdEncoding.EncodeToString(public), Hashes: names}
	if len(nonce) > 0 {
		req.Nonce = base64.StdEncoding.EncodeToString(nonce)
	}
	return json.Marshal(req)
}

func ParseForecast(body []byte, key crypto.PrivateKey) (*Credentials, time.Time, error) {
	var r ForecastReply
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, time.Time{}, fmt.Errorf("cast: the forecast answer: %w", err)
	}

	public, err := base64.StdEncoding.DecodeString(r.Public)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("cast: the forecast public: %w", err)
	}
	leaf, err := x509.ParseCertificate(public)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("cast: the forecast public certificate: %w", err)
	}
	device, err := base64.StdEncoding.DecodeString(r.Device)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("cast: the forecast device: %w", err)
	}
	if _, err := x509.ParseCertificate(device); err != nil {
		return nil, time.Time{}, fmt.Errorf("cast: the forecast device certificate: %w", err)
	}

	encoded := r.Intermediates
	if len(encoded) == 0 && r.Intermediate != "" {
		encoded = []string{r.Intermediate}
	}
	intermediates := make([][]byte, 0, len(encoded))
	for i, enc := range encoded {
		der, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("cast: the forecast intermediate %d: %w", i, err)
		}
		if _, err := x509.ParseCertificate(der); err != nil {
			return nil, time.Time{}, fmt.Errorf(
				"cast: the forecast intermediate %d certificate: %w",
				i,
				err,
			)
		}
		intermediates = append(intermediates, der)
	}

	var sha1, sha256 []byte
	if r.SHA1 != "" {
		if sha1, err = base64.StdEncoding.DecodeString(r.SHA1); err != nil {
			return nil, time.Time{}, fmt.Errorf("cast: the forecast sha1: %w", err)
		}
	}
	if r.SHA256 != "" {
		if sha256, err = base64.StdEncoding.DecodeString(r.SHA256); err != nil {
			return nil, time.Time{}, fmt.Errorf("cast: the forecast sha256: %w", err)
		}
	}

	return &Credentials{
		Peer:          tls.Certificate{Certificate: [][]byte{public}, PrivateKey: key, Leaf: leaf},
		Device:        device,
		Intermediates: intermediates,
		SHA1:          sha1,
		SHA256:        sha256,
		NotBefore:     time.Unix(r.NotBefore, 0),
		NotAfter:      time.Unix(r.NotAfter, 0),
		Remote:        true,
	}, time.Unix(r.Now, 0), nil
}

type Forecast struct {
	URL    string
	Client *http.Client
}

func (f Forecast) Sign(
	ctx context.Context,
	public, nonce []byte,
	hashes []Hash,
	key crypto.PrivateKey,
) (*Credentials, time.Time, error) {
	reqBody, err := ForecastBody(public, nonce, hashes)
	if err != nil {
		return nil, time.Time{}, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		f.at("/api/attest"),
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return nil, time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("cast: forecast: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("cast: forecast %s: %s", f.URL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, forecastMost))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("cast: forecast: %w", err)
	}
	return ParseForecast(body, key)
}

func (f Forecast) at(path string) string { return strings.TrimRight(f.URL, "/") + path }

func (f Forecast) SignInput(ctx context.Context, input string) (string, error) {
	reqBody, err := json.Marshal(map[string]string{"input": input})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		f.at("/api/sign"),
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cast: forecast sign: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, forecastMost))
	if err != nil {
		return "", fmt.Errorf("cast: forecast sign: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error != "" {
			return "", fmt.Errorf("cast: forecast sign: %s", e.Error)
		}
		return "", fmt.Errorf("cast: forecast sign: %s", resp.Status)
	}
	var out struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("cast: forecast sign: %w", err)
	}
	if out.Signature == "" {
		return "", errors.New("cast: forecast sign returned no signature")
	}
	return out.Signature, nil
}

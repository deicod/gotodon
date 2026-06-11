package push

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"time"
)

// vapidTokenExpiration matches Mastodon's WebPushRequest::PAYLOAD_EXPIRATION
// (24 hours, the maximum RFC 8292 permits).
const vapidTokenExpiration = 24 * time.Hour

// VAPIDKey holds the server's VAPID signing key (RFC 8292).
type VAPIDKey struct {
	private   *ecdsa.PrivateKey
	publicB64 string // base64url, unpadded, uncompressed point
}

// NewVAPIDKey builds a signing key from the configured key material: the
// base64-encoded raw P-256 private scalar (32 bytes) and, optionally, the
// matching public key used to cross-check the configuration.
func NewVAPIDKey(privateB64, publicB64 string) (*VAPIDKey, error) {
	scalar, err := decodeBase64(privateB64)
	if err != nil {
		return nil, fmt.Errorf("decoding VAPID private key: %w", err)
	}
	if len(scalar) != 32 {
		return nil, fmt.Errorf("VAPID private key must be 32 bytes, got %d", len(scalar))
	}

	private := new(ecdsa.PrivateKey)
	private.Curve = elliptic.P256()
	private.D = new(big.Int).SetBytes(scalar)
	if private.D.Sign() <= 0 || private.D.Cmp(private.Curve.Params().N) >= 0 {
		return nil, errors.New("invalid VAPID private key scalar")
	}
	private.X, private.Y = private.Curve.ScalarBaseMult(scalar)

	derivedPublic := elliptic.Marshal(elliptic.P256(), private.X, private.Y)
	derivedB64 := base64.RawURLEncoding.EncodeToString(derivedPublic)

	if publicB64 != "" {
		configured, err := decodeBase64(publicB64)
		if err != nil {
			return nil, fmt.Errorf("decoding VAPID public key: %w", err)
		}
		if !bytes.Equal(configured, derivedPublic) {
			return nil, errors.New("VAPID public key does not match private key")
		}
	}

	return &VAPIDKey{private: private, publicB64: derivedB64}, nil
}

// PublicKey returns the unpadded base64url uncompressed public key, as used
// in the Authorization header's k parameter.
func (k *VAPIDKey) PublicKey() string {
	return k.publicB64
}

// AuthorizationHeader builds the RFC 8292 "vapid t=...,k=..." header value
// for a push resource audience.
func (k *VAPIDKey) AuthorizationHeader(audience, subject string, now time.Time) (string, error) {
	token, err := k.signJWT(audience, subject, now)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("vapid t=%s,k=%s", token, k.publicB64), nil
}

func (k *VAPIDKey) signJWT(audience, subject string, now time.Time) (string, error) {
	header, err := json.Marshal(map[string]string{"typ": "JWT", "alg": "ES256"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{
		"aud": audience,
		"exp": now.Add(vapidTokenExpiration).Unix(),
		"sub": subject,
	})
	if err != nil {
		return "", err
	}

	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, k.private, digest[:])
	if err != nil {
		return "", fmt.Errorf("signing VAPID token: %w", err)
	}

	// JWS ES256 signatures are the raw r||s values, each left-padded to 32 bytes.
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// endpointAudience derives the VAPID audience (scheme://host) from a push
// endpoint URL.
func endpointAudience(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid push endpoint: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid push endpoint %q", endpoint)
	}
	return u.Scheme + "://" + u.Host, nil
}

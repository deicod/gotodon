package push

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

// testVAPIDPrivate is a fixed P-256 scalar used only by tests.
const testVAPIDPrivate = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"

func TestNewVAPIDKeyDerivesPublicKey(t *testing.T) {
	key, err := NewVAPIDKey(testVAPIDPrivate, "")
	if err != nil {
		t.Fatalf("NewVAPIDKey: %v", err)
	}
	publicBytes, err := decodeBase64(key.PublicKey())
	if err != nil {
		t.Fatalf("decoding derived public key: %v", err)
	}
	if len(publicBytes) != 65 || publicBytes[0] != 0x04 {
		t.Errorf("expected 65-byte uncompressed point, got %d bytes", len(publicBytes))
	}

	// Passing the matching public key back in must succeed.
	if _, err := NewVAPIDKey(testVAPIDPrivate, key.PublicKey()); err != nil {
		t.Errorf("matching public key rejected: %v", err)
	}
}

func TestNewVAPIDKeyRejectsMismatchedPublicKey(t *testing.T) {
	other, err := NewVAPIDKey("AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA", "")
	if err != nil {
		t.Fatalf("NewVAPIDKey: %v", err)
	}
	if _, err := NewVAPIDKey(testVAPIDPrivate, other.PublicKey()); err == nil {
		t.Error("expected mismatch error")
	}
}

func TestNewVAPIDKeyRejectsBadInput(t *testing.T) {
	if _, err := NewVAPIDKey("", ""); err == nil {
		t.Error("expected error for empty key")
	}
	if _, err := NewVAPIDKey("AAAA", ""); err == nil {
		t.Error("expected error for short key")
	}
}

func TestAuthorizationHeader(t *testing.T) {
	key, err := NewVAPIDKey(testVAPIDPrivate, "")
	if err != nil {
		t.Fatalf("NewVAPIDKey: %v", err)
	}

	now := time.Unix(1700000000, 0)
	header, err := key.AuthorizationHeader("https://push.example.com", "mailto:admin@example.com", now)
	if err != nil {
		t.Fatalf("AuthorizationHeader: %v", err)
	}

	if !strings.HasPrefix(header, "vapid t=") {
		t.Fatalf("unexpected header prefix: %s", header)
	}
	rest := strings.TrimPrefix(header, "vapid t=")
	parts := strings.SplitN(rest, ",k=", 2)
	if len(parts) != 2 {
		t.Fatalf("missing k parameter: %s", header)
	}
	token, publicKey := parts[0], parts[1]
	if publicKey != key.PublicKey() {
		t.Errorf("k parameter mismatch")
	}

	segments := strings.Split(token, ".")
	if len(segments) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(segments))
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(segments[0])
	if err != nil {
		t.Fatalf("decoding JWT header: %v", err)
	}
	var jwtHeader map[string]string
	if err := json.Unmarshal(headerJSON, &jwtHeader); err != nil {
		t.Fatalf("parsing JWT header: %v", err)
	}
	if jwtHeader["alg"] != "ES256" || jwtHeader["typ"] != "JWT" {
		t.Errorf("unexpected JWT header: %v", jwtHeader)
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		t.Fatalf("decoding JWT claims: %v", err)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("parsing JWT claims: %v", err)
	}
	if claims.Aud != "https://push.example.com" {
		t.Errorf("aud = %q", claims.Aud)
	}
	if claims.Sub != "mailto:admin@example.com" {
		t.Errorf("sub = %q", claims.Sub)
	}
	if claims.Exp != now.Add(24*time.Hour).Unix() {
		t.Errorf("exp = %d", claims.Exp)
	}

	// Verify the ES256 signature against the public key.
	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		t.Fatalf("decoding signature: %v", err)
	}
	if len(signature) != 64 {
		t.Fatalf("expected 64-byte signature, got %d", len(signature))
	}
	publicBytes, _ := decodeBase64(key.PublicKey())
	x, y := elliptic.Unmarshal(elliptic.P256(), publicBytes)
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
	digest := sha256.Sum256([]byte(segments[0] + "." + segments[1]))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Error("JWT signature does not verify")
	}
}

func TestEndpointAudience(t *testing.T) {
	audience, err := endpointAudience("https://updates.push.services.mozilla.com/wpush/v2/token123")
	if err != nil {
		t.Fatalf("endpointAudience: %v", err)
	}
	if audience != "https://updates.push.services.mozilla.com" {
		t.Errorf("audience = %q", audience)
	}
	if _, err := endpointAudience("not a url"); err == nil {
		t.Error("expected error for invalid endpoint")
	}
}

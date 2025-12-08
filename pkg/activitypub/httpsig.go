// Package activitypub provides HTTP Signatures for ActivityPub authentication.
// Implements draft-cavage-http-signatures with RSA-SHA256
package activitypub

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPSigner signs HTTP requests using HTTP Signatures
type HTTPSigner struct {
	keyID      string
	privateKey *rsa.PrivateKey
}

// NewHTTPSigner creates a new HTTP signer
func NewHTTPSigner(keyID string, privateKeyPEM string) (*HTTPSigner, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8
		keyInt, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
		var ok bool
		key, ok = keyInt.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key is not RSA")
		}
	}

	return &HTTPSigner{
		keyID:      keyID,
		privateKey: key,
	}, nil
}

// Sign signs an HTTP request
func (s *HTTPSigner) Sign(req *http.Request, body []byte) error {
	// Set Date header if not present
	if req.Header.Get("Date") == "" {
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}

	// Set Digest header for POST/PUT requests with body
	if len(body) > 0 {
		digest := sha256.Sum256(body)
		req.Header.Set("Digest", "SHA-256="+base64.StdEncoding.EncodeToString(digest[:]))
	}

	// Build signature string
	headers := []string{"(request-target)", "host", "date"}
	if len(body) > 0 {
		headers = append(headers, "digest")
	}

	signatureString := s.buildSignatureString(req, headers)

	// Sign with RSA-SHA256
	hashed := sha256.Sum256([]byte(signatureString))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return fmt.Errorf("failed to sign: %w", err)
	}

	// Build Signature header
	signatureHeader := fmt.Sprintf(
		`keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		s.keyID,
		strings.Join(headers, " "),
		base64.StdEncoding.EncodeToString(signature),
	)
	req.Header.Set("Signature", signatureHeader)

	return nil
}

func (s *HTTPSigner) buildSignatureString(req *http.Request, headers []string) string {
	var parts []string
	for _, h := range headers {
		var value string
		switch h {
		case "(request-target)":
			value = fmt.Sprintf("(request-target): %s %s", strings.ToLower(req.Method), req.URL.Path)
		case "host":
			value = fmt.Sprintf("host: %s", req.Host)
		default:
			value = fmt.Sprintf("%s: %s", strings.ToLower(h), req.Header.Get(h))
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n")
}

// HTTPVerifier verifies HTTP Signatures
type HTTPVerifier struct{}

// NewHTTPVerifier creates a new HTTP signature verifier
func NewHTTPVerifier() *HTTPVerifier {
	return &HTTPVerifier{}
}

// VerifyRequest verifies the signature on an incoming request
func (v *HTTPVerifier) VerifyRequest(req *http.Request, publicKeyPEM string) error {
	signatureHeader := req.Header.Get("Signature")
	if signatureHeader == "" {
		return fmt.Errorf("missing Signature header")
	}

	// Parse signature header
	params := v.parseSignatureHeader(signatureHeader)

	keyID := params["keyId"]
	algorithm := params["algorithm"]
	headersStr := params["headers"]
	signatureB64 := params["signature"]

	if keyID == "" || signatureB64 == "" {
		return fmt.Errorf("invalid signature header")
	}

	// Default headers if not specified
	if headersStr == "" {
		headersStr = "date"
	}
	headers := strings.Split(headersStr, " ")

	// Only support rsa-sha256
	if algorithm != "" && algorithm != "rsa-sha256" {
		return fmt.Errorf("unsupported algorithm: %s", algorithm)
	}

	// Parse public key
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return fmt.Errorf("failed to parse public key PEM")
	}

	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPubKey, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("key is not RSA")
	}

	// Build signature string
	signatureString := v.buildSignatureString(req, headers)

	// Decode signature
	signature, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Verify
	hashed := sha256.Sum256([]byte(signatureString))
	err = rsa.VerifyPKCS1v15(rsaPubKey, crypto.SHA256, hashed[:], signature)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

func (v *HTTPVerifier) parseSignatureHeader(header string) map[string]string {
	params := make(map[string]string)
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		idx := strings.Index(part, "=")
		if idx > 0 {
			key := part[:idx]
			value := strings.Trim(part[idx+1:], `"`)
			params[key] = value
		}
	}
	return params
}

func (v *HTTPVerifier) buildSignatureString(req *http.Request, headers []string) string {
	var parts []string
	for _, h := range headers {
		var value string
		switch h {
		case "(request-target)":
			value = fmt.Sprintf("(request-target): %s %s", strings.ToLower(req.Method), req.URL.Path)
		case "host":
			host := req.Host
			if host == "" {
				host = req.URL.Host
			}
			value = fmt.Sprintf("host: %s", host)
		default:
			value = fmt.Sprintf("%s: %s", strings.ToLower(h), req.Header.Get(textproto(h)))
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n")
}

// textproto converts a header name to canonical form
func textproto(h string) string {
	return http.CanonicalHeaderKey(h)
}

// GenerateKeyPair generates a new RSA key pair for signing
func GenerateKeyPair() (*rsa.PrivateKey, *rsa.PublicKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, &privateKey.PublicKey, nil
}

// EncodePrivateKey encodes an RSA private key to PEM format
func EncodePrivateKey(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

// EncodePublicKey encodes an RSA public key to PEM format
func EncodePublicKey(key *rsa.PublicKey) (string, error) {
	pubBytes, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})), nil
}

// SignedClient is an HTTP client that signs requests
type SignedClient struct {
	signer     *HTTPSigner
	httpClient *http.Client
}

// NewSignedClient creates a new signed HTTP client
func NewSignedClient(keyID, privateKeyPEM string) (*SignedClient, error) {
	signer, err := NewHTTPSigner(keyID, privateKeyPEM)
	if err != nil {
		return nil, err
	}

	return &SignedClient{
		signer: signer,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Do performs a signed HTTP request
func (c *SignedClient) Do(req *http.Request, body []byte) (*http.Response, error) {
	// Set body
	if len(body) > 0 {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}

	// Set standard headers
	req.Header.Set("Accept", ActivityPubContentType)
	if len(body) > 0 {
		req.Header.Set("Content-Type", ActivityPubContentType)
	}

	// Sign
	if err := c.signer.Sign(req, body); err != nil {
		return nil, err
	}

	return c.httpClient.Do(req)
}

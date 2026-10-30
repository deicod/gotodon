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
	"net/url"
	"slices"
	"strconv"
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
			path := req.URL.Path
			if path == "" {
				path = "/"
			}
			value = fmt.Sprintf("(request-target): %s %s", strings.ToLower(req.Method), path)
		case "host":
			host := req.Host
			if host == "" {
				host = req.URL.Host
			}
			value = fmt.Sprintf("host: %s", host)
		default:
			value = fmt.Sprintf("%s: %s", strings.ToLower(h), req.Header.Get(h))
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n")
}

// Signature validity limits. They mirror Mastodon's SignedRequest so genuine
// Mastodon and other ActivityPub senders keep verifying.
const (
	// signatureDefaultTTL is how long a signature stays valid after its
	// creation time when no (expires) is signed.
	signatureDefaultTTL = 5 * time.Minute
	// signatureMaxTTL caps a signed (expires) relative to the creation time.
	signatureMaxTTL = 12 * time.Hour
	// signatureClockSkew tolerates clock differences between servers.
	signatureClockSkew = time.Hour
)

// Supported signature algorithms. hs2019 derives the algorithm from the key;
// only RSA keys are supported, so both verify as RSASSA-PKCS1-v1_5 SHA-256.
const (
	algorithmRSASHA256 = "rsa-sha256"
	algorithmHS2019    = "hs2019"
)

// Signature pseudo-headers.
const (
	headerRequestTarget = "(request-target)"
	headerCreated       = "(created)"
	headerExpires       = "(expires)"
)

// HTTPVerifier verifies HTTP Signatures
type HTTPVerifier struct{}

// NewHTTPVerifier creates a new HTTP signature verifier
func NewHTTPVerifier() *HTTPVerifier {
	return &HTTPVerifier{}
}

// RequestSignature is a parsed request signature whose header coverage,
// freshness and body digest have been validated. Verify checks it against
// the signer's public key.
type RequestSignature struct {
	// KeyID is the key the signer claims to have signed with.
	KeyID string
	// Algorithm is the lower-cased signature algorithm.
	Algorithm string
	// Headers lists the signed headers in signing order, lower-cased.
	Headers []string

	params    map[string]string
	signature []byte
	req       *http.Request
}

// VerifyRequest verifies the signature on an incoming request against
// publicKeyPEM. body must be the exact bytes read from req. It is ParseRequest
// followed by Verify.
func (v *HTTPVerifier) VerifyRequest(req *http.Request, body []byte, publicKeyPEM string) error {
	sig, err := v.ParseRequest(req, body)
	if err != nil {
		return err
	}
	return sig.Verify(publicKeyPEM)
}

// ParseRequest parses the Signature header of req and enforces the signature
// profile before any key is fetched:
//   - keyId and signature are present and the algorithm is rsa-sha256 or hs2019;
//   - (request-target), host and either date or (hs2019 only) (created) are signed;
//   - the signed creation time lies within the validity window;
//   - every signed header is present on the request;
//   - POSTs and requests with a body sign Digest, and the signed SHA-256
//     digest matches body.
//
// body must be the exact bytes read from req.
func (v *HTTPVerifier) ParseRequest(req *http.Request, body []byte) (*RequestSignature, error) {
	header := req.Header.Get("Signature")
	if header == "" {
		return nil, fmt.Errorf("missing Signature header")
	}

	params, err := parseSignatureParams(header)
	if err != nil {
		return nil, err
	}
	if params["keyId"] == "" || params["signature"] == "" {
		return nil, fmt.Errorf("signature requires keyId and signature parameters")
	}

	signature, err := base64.StdEncoding.DecodeString(params["signature"])
	if err != nil {
		return nil, fmt.Errorf("failed to decode signature: %w", err)
	}

	// Like Mastodon, treat an absent algorithm as hs2019.
	algorithm := strings.ToLower(params["algorithm"])
	if algorithm == "" {
		algorithm = algorithmHS2019
	}
	if algorithm != algorithmRSASHA256 && algorithm != algorithmHS2019 {
		return nil, fmt.Errorf("unsupported algorithm: %s", algorithm)
	}

	// The draft-cavage default of signing only date (or (created)) gives no
	// request or body integrity, so the headers parameter is mandatory.
	headers := strings.Fields(strings.ToLower(params["headers"]))
	if len(headers) == 0 {
		return nil, fmt.Errorf("signature must list its signed headers")
	}

	sig := &RequestSignature{
		KeyID:     params["keyId"],
		Algorithm: algorithm,
		Headers:   headers,
		params:    params,
		signature: signature,
		req:       req,
	}

	if !sig.covers(headerRequestTarget) {
		return nil, fmt.Errorf("signature must cover (request-target)")
	}
	if !sig.covers("host") {
		return nil, fmt.Errorf("signature must cover host")
	}
	if !sig.covers("date") && !sig.covers(headerCreated) {
		return nil, fmt.Errorf("signature must cover date or (created)")
	}
	if (len(body) > 0 || req.Method == http.MethodPost) && !sig.covers("digest") {
		return nil, fmt.Errorf("signature must cover digest")
	}

	for _, h := range headers {
		if err := sig.checkSignedHeader(h); err != nil {
			return nil, err
		}
	}

	if err := sig.checkFreshness(time.Now()); err != nil {
		return nil, err
	}

	if sig.covers("digest") {
		// Check the exact Digest value that enters the signing string, so the
		// body is bound to the signature.
		if err := verifyBodyDigest(sig.headerValue("digest"), body); err != nil {
			return nil, err
		}
	}

	return sig, nil
}

// Verify checks the signature against publicKeyPEM, a PKIX-encoded RSA public key.
func (s *RequestSignature) Verify(publicKeyPEM string) error {
	key, err := parseRSAPublicKey(publicKeyPEM)
	if err != nil {
		return err
	}

	for _, target := range s.requestTargets() {
		hashed := sha256.Sum256([]byte(s.signingString(target)))
		if rsa.VerifyPKCS1v15(key, crypto.SHA256, hashed[:], s.signature) == nil {
			return nil
		}
	}
	return fmt.Errorf("signature verification failed")
}

func (s *RequestSignature) covers(name string) bool {
	return slices.Contains(s.Headers, name)
}

// checkSignedHeader ensures a signed header or pseudo-header has a value.
func (s *RequestSignature) checkSignedHeader(name string) error {
	switch name {
	case headerRequestTarget:
		return nil
	case headerCreated, headerExpires:
		if s.Algorithm != algorithmHS2019 {
			return fmt.Errorf("pseudo-header %s is not allowed with %s", name, s.Algorithm)
		}
		_, err := s.unixParam(strings.Trim(name, "()"))
		return err
	case "host":
		if s.host() == "" {
			return fmt.Errorf("signed header host is missing")
		}
		return nil
	}

	if strings.HasPrefix(name, "(") {
		return fmt.Errorf("unsupported pseudo-header %s", name)
	}
	if len(s.req.Header.Values(name)) == 0 {
		return fmt.Errorf("signed header %s is missing", name)
	}
	return nil
}

// checkFreshness applies Mastodon's validity window: a signature expires five
// minutes after creation unless it signs (expires), capped at twelve hours,
// and an hour of clock skew is tolerated either way.
func (s *RequestSignature) checkFreshness(now time.Time) error {
	created, err := s.createdAt()
	if err != nil {
		return err
	}

	expires := created.Add(signatureDefaultTTL)
	if s.covers(headerExpires) {
		if expires, err = s.unixParam("expires"); err != nil {
			return err
		}
	}
	if limit := created.Add(signatureMaxTTL); expires.After(limit) {
		expires = limit
	}

	if created.After(now.Add(signatureClockSkew)) {
		return fmt.Errorf("signature created in the future")
	}
	if now.After(expires.Add(signatureClockSkew)) {
		return fmt.Errorf("signature expired")
	}
	return nil
}

// createdAt returns the signed creation time: (created) when signed,
// otherwise the signed Date header.
func (s *RequestSignature) createdAt() (time.Time, error) {
	if s.covers(headerCreated) {
		return s.unixParam("created")
	}
	date, err := http.ParseTime(s.headerValue("date"))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid Date header: %w", err)
	}
	return date, nil
}

func (s *RequestSignature) unixParam(name string) (time.Time, error) {
	seconds, err := strconv.ParseInt(s.params[name], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("signature parameter %s must be a unix timestamp", name)
	}
	return time.Unix(seconds, 0), nil
}

// requestTargets lists the (request-target) paths to try. draft-cavage
// includes the query string, but some senders, including older Mastodon
// releases, sign the path alone.
func (s *RequestSignature) requestTargets() []string {
	path := s.req.URL.Path
	if path == "" {
		path = "/"
	}
	if s.req.URL.RawQuery == "" {
		return []string{path}
	}
	return []string{path + "?" + s.req.URL.RawQuery, path}
}

func (s *RequestSignature) signingString(target string) string {
	lines := make([]string, 0, len(s.Headers))
	for _, h := range s.Headers {
		var value string
		switch h {
		case headerRequestTarget:
			value = strings.ToLower(s.req.Method) + " " + target
		case headerCreated, headerExpires:
			value = s.params[strings.Trim(h, "()")]
		default:
			value = s.headerValue(h)
		}
		lines = append(lines, h+": "+value)
	}
	return strings.Join(lines, "\n")
}

// headerValue returns the value a signed header contributes to the signing
// string. Repeated headers are joined with ", " as draft-cavage requires.
func (s *RequestSignature) headerValue(name string) string {
	if name == "host" {
		return s.host()
	}
	values := s.req.Header.Values(name)
	trimmed := make([]string, len(values))
	for i, value := range values {
		trimmed[i] = strings.TrimSpace(value)
	}
	return strings.Join(trimmed, ", ")
}

func (s *RequestSignature) host() string {
	if s.req.Host != "" {
		return s.req.Host
	}
	return s.req.URL.Host
}

// parseSignatureParams parses the comma-separated name=value list of a
// draft-cavage Signature header. Values are quoted strings (with backslash
// escapes) or bare tokens; malformed input and duplicate names are rejected.
func parseSignatureParams(header string) (map[string]string, error) {
	params := make(map[string]string)
	rest := header
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return params, nil
		}

		name, after, ok := strings.Cut(rest, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, ",\" \t") {
			return nil, fmt.Errorf("malformed signature parameter")
		}
		rest = strings.TrimLeft(after, " \t")

		var value string
		if strings.HasPrefix(rest, `"`) {
			var b strings.Builder
			closed := false
			i := 1
			for ; i < len(rest); i++ {
				c := rest[i]
				if c == '\\' && i+1 < len(rest) {
					i++
					b.WriteByte(rest[i])
					continue
				}
				if c == '"' {
					closed = true
					break
				}
				b.WriteByte(c)
			}
			if !closed {
				return nil, fmt.Errorf("unterminated signature parameter %s", name)
			}
			value = b.String()
			rest = rest[i+1:]
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				end = len(rest)
			}
			value = strings.TrimSpace(rest[:end])
			rest = rest[end:]
		}

		if _, dup := params[name]; dup {
			return nil, fmt.Errorf("duplicate signature parameter %s", name)
		}
		params[name] = value

		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return params, nil
		}
		if rest[0] != ',' {
			return nil, fmt.Errorf("malformed signature parameter %s", name)
		}
		rest = rest[1:]
	}
}

// verifyBodyDigest checks the SHA-256 entries of a Digest header (RFC 3230)
// against body. At least one SHA-256 entry is required and all must match;
// other algorithms are ignored.
func verifyBodyDigest(header string, body []byte) error {
	sum := sha256.Sum256(body)
	actual := base64.StdEncoding.EncodeToString(sum[:])

	found := false
	for _, part := range strings.Split(header, ",") {
		algorithm, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(algorithm), "sha-256") {
			continue
		}
		found = true
		if strings.Trim(strings.TrimSpace(value), `"`) != actual {
			return fmt.Errorf("digest mismatch")
		}
	}
	if !found {
		return fmt.Errorf("digest header has no SHA-256 value")
	}
	return nil
}

func parseRSAPublicKey(publicKeyPEM string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("failed to parse public key PEM")
	}

	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPubKey, ok := pubKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA")
	}
	return rsaPubKey, nil
}

// VerifyKeyOwner checks that keyID names a key of actor, the document
// resolved for actorURI. The document must describe actorURI and own its key,
// and keyID must share the actor's host. When the key's id is known, keyID
// must equal it; keys cached without their id (only the PEM is persisted)
// must be rooted at the actor URI, as in "<actor>#main-key" or
// "<actor>/main-key".
func VerifyKeyOwner(actor *Actor, actorURI, keyID string) error {
	if actor == nil || actor.PublicKey == nil || actor.PublicKey.PublicKeyPem == "" {
		return fmt.Errorf("actor %s has no public key", actorURI)
	}
	if actorURI == "" || actor.ID != actorURI {
		return fmt.Errorf("actor document %q does not match %q", actor.ID, actorURI)
	}
	if owner := actor.PublicKey.Owner; owner != "" && owner != actorURI {
		return fmt.Errorf("key owner %q does not match actor %q", owner, actorURI)
	}
	if !sameHost(keyID, actorURI) {
		return fmt.Errorf("keyId %q is not hosted with actor %q", keyID, actorURI)
	}

	if id := actor.PublicKey.ID; id != "" {
		if id != keyID {
			return fmt.Errorf("keyId %q does not match actor key %q", keyID, id)
		}
		return nil
	}
	if keyID != actorURI && !strings.HasPrefix(keyID, actorURI+"#") && !strings.HasPrefix(keyID, actorURI+"/") {
		return fmt.Errorf("keyId %q does not belong to actor %q", keyID, actorURI)
	}
	return nil
}

func sameHost(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil || ua.Host == "" {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil || ub.Host == "" {
		return false
	}
	return strings.EqualFold(ua.Host, ub.Host)
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

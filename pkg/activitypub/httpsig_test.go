package activitypub

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testActorURI = "https://remote.example/users/alice"
	testKeyID    = testActorURI + "#main-key"
	testInboxURL = "https://local.example/users/bob/inbox"
)

var testBody = []byte(`{"type":"Follow","actor":"https://remote.example/users/alice","object":"https://local.example/users/bob"}`)

type testKeys struct {
	private   *rsa.PrivateKey
	publicPEM string
}

func newTestKeys(t *testing.T) testKeys {
	t.Helper()
	priv, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	publicPEM, err := EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}
	return testKeys{private: priv, publicPEM: publicPEM}
}

// draftSignature describes a draft-cavage signature built independently of
// HTTPSigner, so tests can reproduce what other ActivityPub servers send.
type draftSignature struct {
	keyID     string
	omitKeyID bool
	algorithm string   // omitted when empty
	headers   []string // headers parameter is omitted when nil
	created   string
	expires   string
}

func signDraft(t *testing.T, req *http.Request, key *rsa.PrivateKey, sig draftSignature, target string) {
	t.Helper()

	signed := sig.headers
	if signed == nil {
		signed = []string{"date"}
	}
	lines := make([]string, 0, len(signed))
	for _, h := range signed {
		h = strings.ToLower(h)
		var value string
		switch h {
		case "(request-target)":
			value = strings.ToLower(req.Method) + " " + target
		case "(created)":
			value = sig.created
		case "(expires)":
			value = sig.expires
		case "host":
			value = req.Host
		default:
			value = req.Header.Get(h)
		}
		lines = append(lines, h+": "+value)
	}

	hashed := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15() error = %v", err)
	}

	var params []string
	if !sig.omitKeyID {
		params = append(params, fmt.Sprintf(`keyId="%s"`, sig.keyID))
	}
	if sig.algorithm != "" {
		params = append(params, fmt.Sprintf(`algorithm="%s"`, sig.algorithm))
	}
	if sig.headers != nil {
		params = append(params, fmt.Sprintf(`headers="%s"`, strings.Join(sig.headers, " ")))
	}
	if sig.created != "" {
		params = append(params, "created="+sig.created)
	}
	if sig.expires != "" {
		params = append(params, "expires="+sig.expires)
	}
	params = append(params, fmt.Sprintf(`signature="%s"`, base64.StdEncoding.EncodeToString(signature)))
	req.Header.Set("Signature", strings.Join(params, ","))
}

func bodyDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "SHA-256=" + base64.StdEncoding.EncodeToString(sum[:])
}

func newInboxPost(target string, body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("Digest", bodyDigest(body))
	req.Header.Set("Content-Type", ActivityPubContentType)
	return req
}

func unixString(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}

func TestHTTPVerifier_VerifyRequest(t *testing.T) {
	keys := newTestKeys(t)
	otherKeys := newTestKeys(t)
	mandatory := []string{"(request-target)", "host", "date", "digest"}
	now := time.Now()

	tests := []struct {
		name    string
		target  string
		sig     draftSignature
		key     *rsa.PrivateKey
		before  func(req *http.Request)
		after   func(req *http.Request)
		signFor string // (request-target) path to sign; defaults to the full request target
		body    []byte // body passed to the verifier; defaults to testBody
		wantErr string
	}{
		{
			name: "mastodon header set",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: []string{"(request-target)", "host", "date", "digest", "content-type"}},
		},
		{
			name: "mandatory header set",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
		},
		{
			name: "hs2019 with date",
			sig:  draftSignature{algorithm: "hs2019", headers: mandatory},
		},
		{
			name: "hs2019 with created and expires",
			sig: draftSignature{
				algorithm: "hs2019",
				headers:   []string{"(request-target)", "host", "(created)", "(expires)", "digest"},
				created:   unixString(now.Add(-10 * time.Second)),
				expires:   unixString(now.Add(5 * time.Minute)),
			},
			before: func(req *http.Request) { req.Header.Del("Date") },
		},
		{
			name: "absent algorithm defaults to hs2019",
			sig: draftSignature{
				headers: []string{"(request-target)", "host", "(created)", "digest"},
				created: unixString(now),
			},
		},
		{
			name: "signed content-length",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: []string{"(request-target)", "content-length", "date", "digest", "host"}},
			before: func(req *http.Request) {
				req.Header.Set("Content-Length", strconv.Itoa(len(testBody)))
			},
		},
		{
			name:   "request target with query string",
			target: testInboxURL + "?page=1",
			sig:    draftSignature{algorithm: "rsa-sha256", headers: mandatory},
		},
		{
			name:    "request target signed without query string",
			target:  testInboxURL + "?page=1",
			sig:     draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			signFor: "/users/bob/inbox",
		},
		{
			name: "date within clock skew in the past",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			before: func(req *http.Request) {
				req.Header.Set("Date", now.Add(-time.Hour).UTC().Format(http.TimeFormat))
			},
		},
		{
			name: "date within clock skew in the future",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			before: func(req *http.Request) {
				req.Header.Set("Date", now.Add(50*time.Minute).UTC().Format(http.TimeFormat))
			},
		},
		{
			name: "digest offering several algorithms",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			before: func(req *http.Request) {
				req.Header.Set("Digest", "SHA-512=AAAA,"+bodyDigest(testBody))
			},
		},
		{
			name:    "date-only signature",
			sig:     draftSignature{algorithm: "rsa-sha256", headers: []string{"date"}},
			wantErr: "must cover (request-target)",
		},
		{
			name:    "implicit default headers",
			sig:     draftSignature{algorithm: "rsa-sha256"},
			wantErr: "must list its signed headers",
		},
		{
			name:    "unsigned digest",
			sig:     draftSignature{algorithm: "rsa-sha256", headers: []string{"(request-target)", "host", "date"}},
			wantErr: "must cover digest",
		},
		{
			name:    "missing request target",
			sig:     draftSignature{algorithm: "rsa-sha256", headers: []string{"host", "date", "digest"}},
			wantErr: "must cover (request-target)",
		},
		{
			name:    "missing host",
			sig:     draftSignature{algorithm: "rsa-sha256", headers: []string{"(request-target)", "date", "digest"}},
			wantErr: "must cover host",
		},
		{
			name:    "missing date and created",
			sig:     draftSignature{algorithm: "rsa-sha256", headers: []string{"(request-target)", "host", "digest"}},
			wantErr: "must cover date or (created)",
		},
		{
			name:    "altered body",
			sig:     draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			body:    []byte(`{"type":"Follow","actor":"https://remote.example/users/alice","object":"https://local.example/users/eve"}`),
			wantErr: "digest mismatch",
		},
		{
			name: "digest rewritten to match altered body",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			after: func(req *http.Request) {
				req.Header.Set("Digest", bodyDigest([]byte(`{"type":"Delete"}`)))
			},
			body:    []byte(`{"type":"Delete"}`),
			wantErr: "signature verification failed",
		},
		{
			name: "second digest header appended after signing",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			after: func(req *http.Request) {
				req.Header.Add("Digest", bodyDigest(testBody))
			},
			wantErr: "signature verification failed",
		},
		{
			name: "digest without sha-256",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			before: func(req *http.Request) {
				req.Header.Set("Digest", "SHA-512=AAAA")
			},
			wantErr: "no SHA-256",
		},
		{
			name: "stale date",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			before: func(req *http.Request) {
				req.Header.Set("Date", now.Add(-2*time.Hour).UTC().Format(http.TimeFormat))
			},
			wantErr: "signature expired",
		},
		{
			name: "date too far in the future",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			before: func(req *http.Request) {
				req.Header.Set("Date", now.Add(2*time.Hour).UTC().Format(http.TimeFormat))
			},
			wantErr: "created in the future",
		},
		{
			name: "invalid date",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			before: func(req *http.Request) {
				req.Header.Set("Date", "yesterday")
			},
			wantErr: "invalid Date header",
		},
		{
			name: "expired signed expires",
			sig: draftSignature{
				algorithm: "hs2019",
				headers:   []string{"(request-target)", "host", "(created)", "(expires)", "digest"},
				created:   unixString(now.Add(-2 * time.Hour)),
				expires:   unixString(now.Add(-90 * time.Minute)),
			},
			wantErr: "signature expired",
		},
		{
			name: "created pseudo-header with rsa-sha256",
			sig: draftSignature{
				algorithm: "rsa-sha256",
				headers:   []string{"(request-target)", "host", "(created)", "digest"},
				created:   unixString(now),
			},
			wantErr: "not allowed with rsa-sha256",
		},
		{
			name: "created pseudo-header without parameter",
			sig: draftSignature{
				algorithm: "hs2019",
				headers:   []string{"(request-target)", "host", "(created)", "digest"},
			},
			wantErr: "must be a unix timestamp",
		},
		{
			name:    "unsupported pseudo-header",
			sig:     draftSignature{algorithm: "rsa-sha256", headers: []string{"(request-target)", "host", "date", "digest", "(foo)"}},
			wantErr: "unsupported pseudo-header",
		},
		{
			name: "signed header missing from request",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: []string{"(request-target)", "host", "date", "digest", "content-type"}},
			after: func(req *http.Request) {
				req.Header.Del("Content-Type")
			},
			wantErr: "signed header content-type is missing",
		},
		{
			name:    "unsupported algorithm",
			sig:     draftSignature{algorithm: "hmac-sha256", headers: mandatory},
			wantErr: "unsupported algorithm",
		},
		{
			name:    "signed by another key",
			sig:     draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			key:     otherKeys.private,
			wantErr: "signature verification failed",
		},
		{
			name: "path altered after signing",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			after: func(req *http.Request) {
				req.URL.Path = "/users/eve/inbox"
			},
			wantErr: "signature verification failed",
		},
		{
			name: "host altered after signing",
			sig:  draftSignature{algorithm: "rsa-sha256", headers: mandatory},
			after: func(req *http.Request) {
				req.Host = "other.example"
			},
			wantErr: "signature verification failed",
		},
		{
			name:    "missing keyId",
			sig:     draftSignature{omitKeyID: true, algorithm: "rsa-sha256", headers: mandatory},
			wantErr: "requires keyId and signature",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := tt.target
			if target == "" {
				target = testInboxURL
			}
			req := newInboxPost(target, testBody)
			if tt.before != nil {
				tt.before(req)
			}

			sig := tt.sig
			if sig.keyID == "" {
				sig.keyID = testKeyID
			}
			key := tt.key
			if key == nil {
				key = keys.private
			}
			signFor := tt.signFor
			if signFor == "" {
				signFor = req.URL.RequestURI()
			}
			signDraft(t, req, key, sig, signFor)

			if tt.after != nil {
				tt.after(req)
			}
			body := tt.body
			if body == nil {
				body = testBody
			}

			err := NewHTTPVerifier().VerifyRequest(req, body, keys.publicPEM)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("VerifyRequest() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("VerifyRequest() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestHTTPVerifier_ParseRequest(t *testing.T) {
	keys := newTestKeys(t)
	req := newInboxPost(testInboxURL, testBody)
	signDraft(t, req, keys.private, draftSignature{
		keyID:     testKeyID,
		algorithm: "RSA-SHA256",
		headers:   []string{"(request-target)", "Host", "Date", "Digest"},
	}, req.URL.RequestURI())

	sig, err := NewHTTPVerifier().ParseRequest(req, testBody)
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if sig.KeyID != testKeyID {
		t.Errorf("KeyID = %q, want %q", sig.KeyID, testKeyID)
	}
	if sig.Algorithm != algorithmRSASHA256 {
		t.Errorf("Algorithm = %q, want %q", sig.Algorithm, algorithmRSASHA256)
	}
	if got := strings.Join(sig.Headers, " "); got != "(request-target) host date digest" {
		t.Errorf("Headers = %q", got)
	}

	// The algorithm and headers parameters are case-insensitive; the signing
	// string always uses lower-case header names.
	if err := sig.Verify(keys.publicPEM); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	t.Run("missing signature header", func(t *testing.T) {
		req := newInboxPost(testInboxURL, testBody)
		if _, err := NewHTTPVerifier().ParseRequest(req, testBody); err == nil || !strings.Contains(err.Error(), "missing Signature header") {
			t.Fatalf("ParseRequest() error = %v", err)
		}
	})

	t.Run("invalid public key", func(t *testing.T) {
		req := newInboxPost(testInboxURL, testBody)
		signDraft(t, req, keys.private, draftSignature{keyID: testKeyID, algorithm: "rsa-sha256", headers: []string{"(request-target)", "host", "date", "digest"}}, req.URL.RequestURI())
		sig, err := NewHTTPVerifier().ParseRequest(req, testBody)
		if err != nil {
			t.Fatalf("ParseRequest() error = %v", err)
		}
		if err := sig.Verify("not a pem"); err == nil {
			t.Fatalf("Verify() accepted an invalid key")
		}
		if err := sig.Verify(keys.publicPEM); err != nil {
			t.Fatalf("Verify() error = %v", err)
		}
	})
}

func TestHTTPVerifier_SignerRequests(t *testing.T) {
	keys := newTestKeys(t)
	signer, err := NewHTTPSigner(testKeyID, EncodePrivateKey(keys.private))
	if err != nil {
		t.Fatalf("NewHTTPSigner() error = %v", err)
	}
	verifier := NewHTTPVerifier()

	t.Run("post", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, testInboxURL, bytes.NewReader(testBody))
		if err := signer.Sign(req, testBody); err != nil {
			t.Fatalf("Sign() error = %v", err)
		}
		if err := verifier.VerifyRequest(req, testBody, keys.publicPEM); err != nil {
			t.Fatalf("VerifyRequest() error = %v", err)
		}
	})

	t.Run("get", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "https://local.example/users/bob/followers_synchronization", nil)
		if err := signer.Sign(req, nil); err != nil {
			t.Fatalf("Sign() error = %v", err)
		}
		if err := verifier.VerifyRequest(req, nil, keys.publicPEM); err != nil {
			t.Fatalf("VerifyRequest() error = %v", err)
		}
	})

	t.Run("post without body still requires digest", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, testInboxURL, nil)
		if err := signer.Sign(req, nil); err != nil {
			t.Fatalf("Sign() error = %v", err)
		}
		if err := verifier.VerifyRequest(req, nil, keys.publicPEM); err == nil || !strings.Contains(err.Error(), "must cover digest") {
			t.Fatalf("VerifyRequest() error = %v", err)
		}
	})

	t.Run("get without signed host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "https://local.example/users/bob/followers_synchronization", nil)
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
		signDraft(t, req, keys.private, draftSignature{keyID: testKeyID, algorithm: "rsa-sha256", headers: []string{"(request-target)", "date"}}, req.URL.RequestURI())
		if err := verifier.VerifyRequest(req, nil, keys.publicPEM); err == nil || !strings.Contains(err.Error(), "must cover host") {
			t.Fatalf("VerifyRequest() error = %v", err)
		}
	})
}

func TestParseSignatureParams(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    map[string]string
		wantErr bool
	}{
		{
			name:   "quoted values",
			header: `keyId="https://a.example/users/x#main-key",algorithm="rsa-sha256",headers="(request-target) host",signature="YWJj+/=="`,
			want: map[string]string{
				"keyId":     "https://a.example/users/x#main-key",
				"algorithm": "rsa-sha256",
				"headers":   "(request-target) host",
				"signature": "YWJj+/==",
			},
		},
		{
			name:   "commas and escapes inside quotes",
			header: `keyId="https://a.example/k,1", note="say \"hi\"", created=1402170695 , signature="c2ln"`,
			want: map[string]string{
				"keyId":     "https://a.example/k,1",
				"note":      `say "hi"`,
				"created":   "1402170695",
				"signature": "c2ln",
			},
		},
		{name: "empty", header: "", want: map[string]string{}},
		{name: "unterminated quote", header: `keyId="abc,signature="def"`, wantErr: true},
		{name: "missing equals", header: `keyId`, wantErr: true},
		{name: "empty name", header: `="abc"`, wantErr: true},
		{name: "duplicate parameter", header: `keyId="a",keyId="b"`, wantErr: true},
		{name: "garbage after quoted value", header: `keyId="a"b,signature="c"`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSignatureParams(tt.header)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSignatureParams() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSignatureParams() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseSignatureParams() = %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("param %s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestVerifyKeyOwner(t *testing.T) {
	actorWithKey := func(id string, key *PublicKey) *Actor {
		return &Actor{Object: Object{ID: id}, PublicKey: key}
	}
	fetchedKey := &PublicKey{ID: testKeyID, Owner: testActorURI, PublicKeyPem: "pem"}
	cachedKey := &PublicKey{Owner: testActorURI, PublicKeyPem: "pem"}

	tests := []struct {
		name     string
		actor    *Actor
		actorURI string
		keyID    string
		wantErr  bool
	}{
		{name: "fetched key matches", actor: actorWithKey(testActorURI, fetchedKey), actorURI: testActorURI, keyID: testKeyID},
		{name: "cached key with fragment id", actor: actorWithKey(testActorURI, cachedKey), actorURI: testActorURI, keyID: testKeyID},
		{name: "cached key with path id", actor: actorWithKey(testActorURI, cachedKey), actorURI: testActorURI, keyID: testActorURI + "/main-key"},
		{name: "cached key with slash fragment id", actor: actorWithKey(testActorURI, cachedKey), actorURI: testActorURI, keyID: testActorURI + "/#main-key"},
		{name: "cached key with actor id", actor: actorWithKey(testActorURI, cachedKey), actorURI: testActorURI, keyID: testActorURI},
		{name: "cached key without owner", actor: actorWithKey(testActorURI, &PublicKey{PublicKeyPem: "pem"}), actorURI: testActorURI, keyID: testKeyID},
		{name: "fetched key id differs", actor: actorWithKey(testActorURI, fetchedKey), actorURI: testActorURI, keyID: testActorURI + "#other-key", wantErr: true},
		{name: "keyId of another actor", actor: actorWithKey(testActorURI, cachedKey), actorURI: testActorURI, keyID: "https://remote.example/users/mallory#main-key", wantErr: true},
		{name: "keyId sharing actor prefix", actor: actorWithKey(testActorURI, cachedKey), actorURI: testActorURI, keyID: testActorURI + "x#main-key", wantErr: true},
		{name: "keyId on another host", actor: actorWithKey(testActorURI, cachedKey), actorURI: testActorURI, keyID: "https://evil.example/users/alice#main-key", wantErr: true},
		{name: "keyId not a URL", actor: actorWithKey(testActorURI, cachedKey), actorURI: testActorURI, keyID: "main-key", wantErr: true},
		{name: "actor document for another uri", actor: actorWithKey("https://remote.example/users/mallory", fetchedKey), actorURI: testActorURI, keyID: testKeyID, wantErr: true},
		{name: "actor document without id", actor: actorWithKey("", fetchedKey), actorURI: testActorURI, keyID: testKeyID, wantErr: true},
		{name: "key owned by another actor", actor: actorWithKey(testActorURI, &PublicKey{ID: testKeyID, Owner: "https://remote.example/users/mallory", PublicKeyPem: "pem"}), actorURI: testActorURI, keyID: testKeyID, wantErr: true},
		{name: "actor without key", actor: actorWithKey(testActorURI, nil), actorURI: testActorURI, keyID: testKeyID, wantErr: true},
		{name: "actor with empty key", actor: actorWithKey(testActorURI, &PublicKey{ID: testKeyID}), actorURI: testActorURI, keyID: testKeyID, wantErr: true},
		{name: "nil actor", actor: nil, actorURI: testActorURI, keyID: testKeyID, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyKeyOwner(tt.actor, tt.actorURI, tt.keyID)
			if tt.wantErr && err == nil {
				t.Fatalf("VerifyKeyOwner() succeeded, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("VerifyKeyOwner() error = %v", err)
			}
		})
	}
}

type mapActorFetcher map[string]*Actor

func (f mapActorFetcher) FetchActor(ctx context.Context, actorURI string) (*Actor, error) {
	actor, ok := f[actorURI]
	if !ok {
		return nil, fmt.Errorf("actor %s not found", actorURI)
	}
	return actor, nil
}

func TestInboxHandler_HandleInbox(t *testing.T) {
	keys := newTestKeys(t)
	mandatory := []string{"(request-target)", "host", "date", "digest"}
	actor := &Actor{
		Object:    Object{ID: testActorURI},
		PublicKey: &PublicKey{ID: testKeyID, Owner: testActorURI, PublicKeyPem: keys.publicPEM},
	}

	tests := []struct {
		name         string
		fetcher      mapActorFetcher
		sig          draftSignature
		wantErr      string
		wantDispatch bool
	}{
		{
			name:         "valid signature",
			fetcher:      mapActorFetcher{testActorURI: actor},
			sig:          draftSignature{keyID: testKeyID, algorithm: "rsa-sha256", headers: mandatory},
			wantDispatch: true,
		},
		{
			name:    "date-only signature",
			fetcher: mapActorFetcher{testActorURI: actor},
			sig:     draftSignature{keyID: testKeyID, algorithm: "rsa-sha256", headers: []string{"date"}},
			wantErr: "must cover (request-target)",
		},
		{
			name:    "keyId does not match actor key",
			fetcher: mapActorFetcher{testActorURI: actor},
			sig:     draftSignature{keyID: "https://remote.example/users/mallory#main-key", algorithm: "rsa-sha256", headers: mandatory},
			wantErr: "does not match actor key",
		},
		{
			name: "actor without public key",
			fetcher: mapActorFetcher{testActorURI: {
				Object: Object{ID: testActorURI},
			}},
			sig:     draftSignature{keyID: testKeyID, algorithm: "rsa-sha256", headers: mandatory},
			wantErr: "has no public key",
		},
		{
			name:    "unknown actor",
			fetcher: mapActorFetcher{},
			sig:     draftSignature{keyID: testKeyID, algorithm: "rsa-sha256", headers: mandatory},
			wantErr: "failed to fetch actor",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dispatched := false
			handler := NewInboxHandler(tt.fetcher)
			handler.RegisterHandler(TypeFollow, func(ctx context.Context, activity *Activity) error {
				dispatched = true
				return nil
			})

			req := newInboxPost(testInboxURL, testBody)
			signDraft(t, req, keys.private, tt.sig, req.URL.RequestURI())

			err := handler.HandleInbox(context.Background(), req, testBody)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("HandleInbox() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("HandleInbox() error = %v, want %q", err, tt.wantErr)
			}
			if dispatched != tt.wantDispatch {
				t.Fatalf("dispatched = %v, want %v", dispatched, tt.wantDispatch)
			}
		})
	}
}

package middleware

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deicod/gotodon/pkg/activitypub"
)

const (
	sigActorURI = "https://remote.example/users/alice"
	sigKeyID    = sigActorURI + "#main-key"
	sigInboxURL = "https://local.example/users/bob/inbox"
)

var sigMandatoryHeaders = []string{"(request-target)", "host", "date", "digest"}

type recordingActorFetcher struct {
	actors map[string]*activitypub.Actor
	err    error
	calls  int
}

func (f *recordingActorFetcher) FetchActor(ctx context.Context, actorURI string) (*activitypub.Actor, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	actor, ok := f.actors[actorURI]
	if !ok {
		return nil, fmt.Errorf("actor %s not found", actorURI)
	}
	return actor, nil
}

func generateSigningKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	publicPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}
	return priv, publicPEM
}

func sha256Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "SHA-256=" + base64.StdEncoding.EncodeToString(sum[:])
}

// newSignedInboxPost builds an inbox POST signed over headers the way remote
// ActivityPub servers do, independently of activitypub.HTTPSigner.
func newSignedInboxPost(t *testing.T, key *rsa.PrivateKey, keyID string, headers []string, body []byte) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, sigInboxURL, bytes.NewReader(body))
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("Digest", sha256Digest(body))
	req.Header.Set("Content-Type", activitypub.ActivityPubContentType)

	lines := make([]string, 0, len(headers))
	for _, h := range headers {
		switch h {
		case "(request-target)":
			lines = append(lines, h+": post "+req.URL.RequestURI())
		case "host":
			lines = append(lines, h+": "+req.Host)
		default:
			lines = append(lines, h+": "+req.Header.Get(h))
		}
	}
	hashed := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15() error = %v", err)
	}

	req.Header.Set("Signature", fmt.Sprintf(
		`keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		keyID, strings.Join(headers, " "), base64.StdEncoding.EncodeToString(signature),
	))
	return req
}

func followActivity(actor string) []byte {
	return []byte(`{"type":"Follow","actor":"` + actor + `","object":"https://local.example/users/bob"}`)
}

func TestVerifyHTTPSignature(t *testing.T) {
	aliceKey, alicePEM := generateSigningKey(t)
	malloryKey, malloryPEM := generateSigningKey(t)

	malloryURI := "https://remote.example/users/mallory"
	alice := &activitypub.Actor{
		Object:    activitypub.Object{ID: sigActorURI},
		PublicKey: &activitypub.PublicKey{ID: sigKeyID, Owner: sigActorURI, PublicKeyPem: alicePEM},
	}
	mallory := &activitypub.Actor{
		Object:    activitypub.Object{ID: malloryURI},
		PublicKey: &activitypub.PublicKey{ID: malloryURI + "#main-key", Owner: malloryURI, PublicKeyPem: malloryPEM},
	}
	actors := map[string]*activitypub.Actor{sigActorURI: alice, malloryURI: mallory}

	aliceBody := followActivity(sigActorURI)

	tests := []struct {
		name        string
		request     func(t *testing.T) *http.Request
		actors      map[string]*activitypub.Actor
		fetchErr    error
		wantStatus  int
		wantFetches int
	}{
		{
			name: "valid mastodon-style signature",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, []string{"(request-target)", "host", "date", "digest", "content-type"}, aliceBody)
			},
			wantStatus:  http.StatusOK,
			wantFetches: 1,
		},
		{
			name: "valid signature from activitypub signer",
			request: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, sigInboxURL, bytes.NewReader(aliceBody))
				signer, err := activitypub.NewHTTPSigner(sigKeyID, activitypub.EncodePrivateKey(aliceKey))
				if err != nil {
					t.Fatalf("NewHTTPSigner() error = %v", err)
				}
				if err := signer.Sign(req, aliceBody); err != nil {
					t.Fatalf("Sign() error = %v", err)
				}
				return req
			},
			wantStatus:  http.StatusOK,
			wantFetches: 1,
		},
		{
			name: "valid signature with cached key and path key id",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigActorURI+"/main-key", sigMandatoryHeaders, aliceBody)
			},
			actors: map[string]*activitypub.Actor{sigActorURI: {
				Object:    activitypub.Object{ID: sigActorURI},
				PublicKey: &activitypub.PublicKey{Owner: sigActorURI, PublicKeyPem: alicePEM},
			}},
			wantStatus:  http.StatusOK,
			wantFetches: 1,
		},
		{
			name: "date-only signature",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, []string{"date"}, aliceBody)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "signature without headers parameter",
			request: func(t *testing.T) *http.Request {
				req := newSignedInboxPost(t, aliceKey, sigKeyID, []string{"date"}, aliceBody)
				sig := req.Header.Get("Signature")
				req.Header.Set("Signature", strings.Replace(sig, `headers="date",`, "", 1))
				return req
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "unsigned digest",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, []string{"(request-target)", "host", "date"}, aliceBody)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "missing signed request target",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, []string{"host", "date", "digest"}, aliceBody)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "missing signed host",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, []string{"(request-target)", "date", "digest"}, aliceBody)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "missing signed date",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, []string{"(request-target)", "host", "digest"}, aliceBody)
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "stale date",
			request: func(t *testing.T) *http.Request {
				req := newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, aliceBody)
				// Re-sign with a stale Date so the signature itself is valid.
				stale := time.Now().Add(-2 * time.Hour).UTC().Format(http.TimeFormat)
				signer, err := activitypub.NewHTTPSigner(sigKeyID, activitypub.EncodePrivateKey(aliceKey))
				if err != nil {
					t.Fatalf("NewHTTPSigner() error = %v", err)
				}
				req.Header.Set("Date", stale)
				if err := signer.Sign(req, aliceBody); err != nil {
					t.Fatalf("Sign() error = %v", err)
				}
				return req
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "date too far in the future",
			request: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, sigInboxURL, bytes.NewReader(aliceBody))
				signer, err := activitypub.NewHTTPSigner(sigKeyID, activitypub.EncodePrivateKey(aliceKey))
				if err != nil {
					t.Fatalf("NewHTTPSigner() error = %v", err)
				}
				req.Header.Set("Date", time.Now().Add(2*time.Hour).UTC().Format(http.TimeFormat))
				if err := signer.Sign(req, aliceBody); err != nil {
					t.Fatalf("Sign() error = %v", err)
				}
				return req
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "altered body",
			request: func(t *testing.T) *http.Request {
				req := newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, aliceBody)
				tampered := []byte(`{"type":"Follow","actor":"` + sigActorURI + `","object":"https://local.example/users/eve"}`)
				req.Body = io.NopCloser(bytes.NewReader(tampered))
				req.ContentLength = int64(len(tampered))
				return req
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "altered body with recomputed unsigned digest",
			request: func(t *testing.T) *http.Request {
				req := newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, aliceBody)
				tampered := []byte(`{"type":"Delete","actor":"` + sigActorURI + `","object":"` + sigActorURI + `"}`)
				req.Body = io.NopCloser(bytes.NewReader(tampered))
				req.ContentLength = int64(len(tampered))
				req.Header.Set("Digest", sha256Digest(tampered))
				return req
			},
			wantStatus:  http.StatusUnauthorized,
			wantFetches: 1,
		},
		{
			name: "keyId of another actor signed with actor key",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, malloryURI+"#main-key", sigMandatoryHeaders, aliceBody)
			},
			wantStatus:  http.StatusUnauthorized,
			wantFetches: 1,
		},
		{
			name: "activity actor differs from signer",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, malloryKey, malloryURI+"#main-key", sigMandatoryHeaders, aliceBody)
			},
			wantStatus:  http.StatusUnauthorized,
			wantFetches: 1,
		},
		{
			name: "actor document for another actor",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, malloryKey, malloryURI+"#main-key", sigMandatoryHeaders, aliceBody)
			},
			actors:      map[string]*activitypub.Actor{sigActorURI: mallory},
			wantStatus:  http.StatusUnauthorized,
			wantFetches: 1,
		},
		{
			name: "signed by another key",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, malloryKey, sigKeyID, sigMandatoryHeaders, aliceBody)
			},
			wantStatus:  http.StatusUnauthorized,
			wantFetches: 1,
		},
		{
			name: "actor fetch fails",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, aliceBody)
			},
			fetchErr:    errors.New("unreachable"),
			wantStatus:  http.StatusUnauthorized,
			wantFetches: 1,
		},
		{
			name: "actor without public key",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, aliceBody)
			},
			actors:      map[string]*activitypub.Actor{sigActorURI: {Object: activitypub.Object{ID: sigActorURI}}},
			wantStatus:  http.StatusUnauthorized,
			wantFetches: 1,
		},
		{
			name: "missing signature",
			request: func(t *testing.T) *http.Request {
				req := newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, aliceBody)
				req.Header.Del("Signature")
				return req
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "malformed signature",
			request: func(t *testing.T) *http.Request {
				req := newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, aliceBody)
				req.Header.Set("Signature", `keyId="`+sigKeyID+`,signature="abc`)
				return req
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "empty body",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, nil)
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "invalid activity json",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, []byte(`not json`))
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "activity without actor",
			request: func(t *testing.T) *http.Request {
				return newSignedInboxPost(t, aliceKey, sigKeyID, sigMandatoryHeaders, []byte(`{"type":"Follow"}`))
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcherActors := tt.actors
			if fetcherActors == nil {
				fetcherActors = actors
			}
			fetcher := &recordingActorFetcher{actors: fetcherActors, err: tt.fetchErr}

			var nextBody []byte
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			})

			req := tt.request(t)
			rec := httptest.NewRecorder()
			VerifyHTTPSignature(fetcher)(next).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if fetcher.calls != tt.wantFetches {
				t.Fatalf("actor fetches = %d, want %d", fetcher.calls, tt.wantFetches)
			}
			if tt.wantStatus == http.StatusOK && !bytes.Equal(nextBody, aliceBody) {
				t.Fatalf("next handler body = %q, want %q", nextBody, aliceBody)
			}
		})
	}
}

func TestVerifyHTTPSignature_SignedActivity(t *testing.T) {
	key, publicPEM := generateSigningKey(t)
	fetcher := &recordingActorFetcher{actors: map[string]*activitypub.Actor{
		sigActorURI: {
			Object:    activitypub.Object{ID: sigActorURI},
			PublicKey: &activitypub.PublicKey{ID: sigKeyID, Owner: sigActorURI, PublicKeyPem: publicPEM},
		},
	}}

	var got SignedActivity
	var ok bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = SignedActivityFromContext(r.Context())
		w.WriteHeader(http.StatusAccepted)
	})

	body := followActivity(sigActorURI)
	req := newSignedInboxPost(t, key, sigKeyID, sigMandatoryHeaders, body)
	date, err := http.ParseTime(req.Header.Get("Date"))
	if err != nil {
		t.Fatalf("ParseTime() error = %v", err)
	}

	rec := httptest.NewRecorder()
	VerifyHTTPSignature(fetcher)(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if !ok {
		t.Fatal("verified request carries no SignedActivity")
	}
	if got.ActorURI != sigActorURI {
		t.Errorf("ActorURI = %q, want %q", got.ActorURI, sigActorURI)
	}
	if !bytes.Equal(got.Body, body) {
		t.Errorf("Body = %q, want %q", got.Body, body)
	}
	if got.BodyDigest != sha256.Sum256(body) {
		t.Errorf("BodyDigest does not match the body")
	}
	if want := date.Add(5*time.Minute + time.Hour); !got.ValidUntil.Equal(want) {
		t.Errorf("ValidUntil = %v, want %v", got.ValidUntil, want)
	}

	if _, ok := SignedActivityFromContext(context.Background()); ok {
		t.Error("SignedActivityFromContext() found an activity in an empty context")
	}
}

func TestVerifyHTTPSignature_NilFetcher(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run")
	})

	req := httptest.NewRequest(http.MethodPost, sigInboxURL, strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	VerifyHTTPSignature(nil)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

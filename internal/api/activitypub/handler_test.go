package activitypub

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/pkg/activitypub"
)

type stubActorFetcher struct {
	actor *activitypub.Actor
	err   error
}

func (s stubActorFetcher) FetchActor(ctx context.Context, actorURI string) (*activitypub.Actor, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.actor, nil
}

func signedInboxRequest(t *testing.T, body []byte, keyID, privateKeyPEM string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/inbox", bytes.NewReader(body))
	signer, err := activitypub.NewHTTPSigner(keyID, privateKeyPEM)
	if err != nil {
		t.Fatalf("NewHTTPSigner() error = %v", err)
	}
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return req
}

func TestFederationHandler_PostInbox_VerifiesSignature(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	activity := activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: "https://example.com/users/bob",
	}
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

	handler := &FederationHandler{
		domain: "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
			PublicKey: &activitypub.PublicKey{
				ID:           actorURI + "#main-key",
				Owner:        actorURI,
				PublicKeyPem: publicKeyPEM,
			},
		}},
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
}

func TestFederationHandler_PostInbox_MissingSignature(t *testing.T) {
	activity := activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          "https://remote.example/users/alice",
		ActivityObject: "https://example.com/users/bob",
	}
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "https://example.com/inbox", bytes.NewReader(body))

	handler := &FederationHandler{
		domain:       "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{}},
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestFederationHandler_PostInbox_DigestMismatch(t *testing.T) {
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	privateKeyPEM := activitypub.EncodePrivateKey(priv)
	publicKeyPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}

	actorURI := "https://remote.example/users/alice"
	activity := activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          actorURI,
		ActivityObject: "https://example.com/users/bob",
	}
	body, err := json.Marshal(activity)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

	tampered := []byte(`{"type":"Follow","actor":"https://remote.example/users/alice","object":"https://example.com/users/eve"}`)
	req.Body = io.NopCloser(bytes.NewReader(tampered))
	req.ContentLength = int64(len(tampered))

	handler := &FederationHandler{
		domain: "example.com",
		actorFetcher: stubActorFetcher{actor: &activitypub.Actor{
			PublicKey: &activitypub.PublicKey{
				ID:           actorURI + "#main-key",
				Owner:        actorURI,
				PublicKeyPem: publicKeyPEM,
			},
		}},
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

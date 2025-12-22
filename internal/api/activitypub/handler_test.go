package activitypub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestFederationHandler_PostInbox_RoutesActivity(t *testing.T) {
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

	handled := false
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
	handler.RegisterInboxHandler(activitypub.TypeFollow, func(ctx context.Context, activity *activitypub.Activity) error {
		handled = true
		if activity.Type != activitypub.TypeFollow {
			t.Errorf("activity type = %q, want %q", activity.Type, activitypub.TypeFollow)
		}
		return nil
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if !handled {
		t.Fatal("expected handler to be called")
	}
}

func TestFederationHandler_PostInbox_RoutesAcceptReject(t *testing.T) {
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
	localURI := "https://example.com/users/bob"

	cases := []struct {
		name         string
		activityType string
	}{
		{name: "accept", activityType: activitypub.TypeAccept},
		{name: "reject", activityType: activitypub.TypeReject},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{
				"type":  tc.activityType,
				"actor": actorURI,
				"object": map[string]any{
					"type":   activitypub.TypeFollow,
					"actor":  localURI,
					"object": actorURI,
				},
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

			handled := false
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
			handler.RegisterInboxHandler(tc.activityType, func(ctx context.Context, activity *activitypub.Activity) error {
				handled = true
				if activity.Type != tc.activityType {
					t.Errorf("activity type = %q, want %q", activity.Type, tc.activityType)
				}
				object, ok := activity.ActivityObject.(map[string]any)
				if !ok {
					t.Fatalf("activity object type = %T, want map", activity.ActivityObject)
				}
				if objType, ok := object["type"].(string); !ok || objType != activitypub.TypeFollow {
					t.Errorf("follow type = %v, want %s", object["type"], activitypub.TypeFollow)
				}
				if object["actor"] != localURI {
					t.Errorf("follow actor = %v, want %s", object["actor"], localURI)
				}
				if object["object"] != actorURI {
					t.Errorf("follow object = %v, want %s", object["object"], actorURI)
				}
				return nil
			})

			mux := http.NewServeMux()
			handler.RegisterRoutes(mux)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
			}
			if !handled {
				t.Fatal("expected handler to be called")
			}
		})
	}
}

func TestFederationHandler_PostInbox_ArrayTypeRoutes(t *testing.T) {
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
	body := []byte(fmt.Sprintf(`{"type":["Activity","Follow"],"actor":"%s","object":"https://example.com/users/bob"}`, actorURI))
	req := signedInboxRequest(t, body, actorURI+"#main-key", privateKeyPEM)

	handled := false
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
	handler.RegisterInboxHandler(activitypub.TypeFollow, func(ctx context.Context, activity *activitypub.Activity) error {
		handled = true
		if activity.Type != activitypub.TypeFollow {
			t.Errorf("activity type = %q, want %q", activity.Type, activitypub.TypeFollow)
		}
		return nil
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if !handled {
		t.Fatal("expected handler to be called")
	}
}

func TestFederationHandler_PostInbox_MissingType(t *testing.T) {
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
	body := []byte(`{"actor":"` + actorURI + `","object":"https://example.com/users/bob"}`)

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

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestFederationHandler_PostInbox_HandlerErrorStatus(t *testing.T) {
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
	handler.RegisterInboxHandler(activitypub.TypeFollow, func(ctx context.Context, activity *activitypub.Activity) error {
		return &InboxError{Status: http.StatusUnprocessableEntity, Message: "Unprocessable activity"}
	})

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

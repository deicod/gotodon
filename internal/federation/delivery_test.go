package federation

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

func TestDeliveryService_DeliversSignedRequest(t *testing.T) {
	privateKey, publicKey, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair error: %v", err)
	}
	privatePEM := activitypub.EncodePrivateKey(privateKey)
	publicPEM, err := activitypub.EncodePublicKey(publicKey)
	if err != nil {
		t.Fatalf("EncodePublicKey error: %v", err)
	}

	var gotSignature, gotDigest string
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		gotSignature = r.Header.Get("Signature")
		gotDigest = r.Header.Get("Digest")

		verifier := activitypub.NewHTTPVerifier()
		if err := verifier.VerifyRequest(r, publicPEM); err != nil {
			t.Errorf("VerifyRequest error: %v", err)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	local := db.Account{
		Uri:        "https://local.test/users/alice",
		PrivateKey: pgtype.Text{String: privatePEM, Valid: true},
	}
	activity := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          local.Uri,
		ActivityObject: "https://remote.example/users/bob",
	}

	service := NewDeliveryService()
	if err := service.DeliverActivity(context.Background(), local, server.URL, activity); err != nil {
		t.Fatalf("DeliverActivity error: %v", err)
	}

	if gotSignature == "" {
		t.Fatalf("expected Signature header")
	}
	if gotDigest == "" {
		t.Fatalf("expected Digest header")
	}
	if len(gotBody) == 0 {
		t.Fatalf("expected request body")
	}
}

func TestDeliveryService_ValidatesInputs(t *testing.T) {
	service := NewDeliveryService()

	err := service.DeliverActivity(context.Background(), db.Account{}, "https://example.com/inbox", &activitypub.Activity{})
	if err == nil {
		t.Fatalf("expected error for missing actor data")
	}

	err = service.DeliverActivity(context.Background(), db.Account{Uri: "https://local.test/users/alice"}, "", &activitypub.Activity{})
	if err == nil {
		t.Fatalf("expected error for missing inbox")
	}

	err = service.DeliverActivity(context.Background(), db.Account{
		Uri:        "https://local.test/users/alice",
		PrivateKey: pgtype.Text{String: "key", Valid: true},
	}, "https://example.com/inbox", nil)
	if err == nil {
		t.Fatalf("expected error for missing activity")
	}
}

func TestDeliveryService_PropagatesFailureStatus(t *testing.T) {
	privateKey, _, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair error: %v", err)
	}
	privatePEM := activitypub.EncodePrivateKey(privateKey)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	local := db.Account{
		Uri:        "https://local.test/users/alice",
		PrivateKey: pgtype.Text{String: privatePEM, Valid: true},
	}
	activity := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          local.Uri,
		ActivityObject: "https://remote.example/users/bob",
	}

	service := NewDeliveryService()
	if err := service.DeliverActivity(context.Background(), local, server.URL, activity); err == nil {
		t.Fatalf("expected error for non-2xx response")
	}
}

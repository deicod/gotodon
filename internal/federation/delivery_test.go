package federation

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/safehttp"
	"github.com/deicod/gotodon/pkg/safehttp/safehttptest"
)

// routedDeliveryService delivers through a guarded client that reaches each
// test server under its host name.
func routedDeliveryService(t *testing.T, servers map[string]*httptest.Server) *DeliveryService {
	client := safehttptest.Client(t, servers)
	return &DeliveryService{
		newOutbox: func(keyID, privateKeyPEM string) (*activitypub.OutboxHandler, error) {
			return activitypub.NewOutboxHandlerWithHTTPClient(keyID, privateKeyPEM, client)
		},
	}
}

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

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		gotSignature = r.Header.Get("Signature")
		gotDigest = r.Header.Get("Digest")

		verifier := activitypub.NewHTTPVerifier()
		if err := verifier.VerifyRequest(r, body, publicPEM); err != nil {
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

	service := routedDeliveryService(t, map[string]*httptest.Server{"inbox.example.com": server})
	if err := service.DeliverActivity(context.Background(), local, "https://inbox.example.com/inbox", activity); err != nil {
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

	service := routedDeliveryService(t, map[string]*httptest.Server{"remote.example": server})
	err = service.DeliverActivity(context.Background(), local, "http://remote.example/inbox", activity)
	if err == nil || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("DeliverActivity error = %v, want the 502 status", err)
	}
}

func TestDeliveryService_RefusesInboxesOnNonPublicAddresses(t *testing.T) {
	privateKey, _, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair error: %v", err)
	}
	var hits atomic.Int64
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer internal.Close()
	port := internal.URL[strings.LastIndex(internal.URL, ":")+1:]

	local := db.Account{
		Uri:        "https://local.test/users/alice",
		PrivateKey: pgtype.Text{String: activitypub.EncodePrivateKey(privateKey), Valid: true},
	}
	activity := &activitypub.Activity{
		Type:           activitypub.TypeFollow,
		Actor:          local.Uri,
		ActivityObject: "https://remote.example/users/bob",
	}

	service := NewDeliveryService()
	for _, inbox := range []string{
		internal.URL + "/inbox",
		"http://localhost:" + port + "/inbox",
		"http://[::1]:" + port + "/inbox",
		"http://169.254.169.254/inbox",
		"http://10.0.0.1/inbox",
	} {
		err := service.DeliverActivity(context.Background(), local, inbox, activity)
		if !errors.Is(err, safehttp.ErrForbiddenAddress) {
			t.Errorf("DeliverActivity(%s) error = %v, want ErrForbiddenAddress", inbox, err)
		}
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("internal inbox received %d deliveries, want 0", got)
	}
}

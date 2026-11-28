package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// cachedActorStore holds the remote accounts that the inbox's actor fetcher
// finds cached, so signed deliveries verify without fetching. The fetcher
// only looks accounts up by URI.
type cachedActorStore struct {
	apihandler.Store
	accounts map[string]db.Account
}

func (s cachedActorStore) GetAccountByURI(ctx context.Context, uri string) (db.Account, error) {
	account, ok := s.accounts[uri]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

type inboxKey struct {
	id, privatePEM string
}

func newInboxKey(t *testing.T, actorURI string) (inboxKey, string) {
	t.Helper()
	priv, pub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	publicPEM, err := activitypub.EncodePublicKey(pub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}
	return inboxKey{id: actorURI + "#main-key", privatePEM: activitypub.EncodePrivateKey(priv)}, publicPEM
}

// TestFlagInbox_SignedDeliveries delivers Flags through the inbox routes:
// only a Flag that its actor signed files a report, and only once.
func TestFlagInbox_SignedDeliveries(t *testing.T) {
	f := newFlagFixture(t)

	reporterKey, reporterPublic := newInboxKey(t, flagActorURI)
	carolKey, carolPublic := newInboxKey(t, f.carol.Uri)
	strangerKey, _ := newInboxKey(t, flagActorURI)
	f.reporter.PublicKey = reporterPublic
	f.carol.PublicKey = carolPublic
	f.store.addAccount(f.reporter)
	f.store.addAccount(f.carol)

	fed := apihandler.NewFederationHandler(cachedActorStore{accounts: map[string]db.Account{
		f.reporter.Uri: f.reporter,
		f.carol.Uri:    f.carol,
	}}, flagLocalDomain)
	service := NewFlagService(f.store, flagLocalDomain, fed.ActorFetcher())
	service.SetMailDeliverer(f.mail)
	fed.RegisterInboxHandler(activitypub.TypeFlag, service.HandleFlag)
	mux := http.NewServeMux()
	fed.RegisterRoutes(mux)

	flagBody := func(id, actor string) []byte {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams",
			"id":       id,
			"type":     "Flag",
			"actor":    actor,
			"content":  "Spam",
			"object":   []string{f.alice.Uri, f.alicePublic.Uri.String},
		})
		if err != nil {
			t.Fatalf("marshal flag: %v", err)
		}
		return body
	}
	// deliver posts body to path, signed over signed with key unless key is
	// the zero key, and returns the response status.
	deliver := func(path string, body, signed []byte, key inboxKey) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "https://"+flagLocalDomain+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", activitypub.ActivityPubContentType)
		if key.id != "" {
			signer, err := activitypub.NewHTTPSigner(key.id, key.privatePEM)
			if err != nil {
				t.Fatalf("NewHTTPSigner() error = %v", err)
			}
			if err := signer.Sign(req, signed); err != nil {
				t.Fatalf("Sign() error = %v", err)
			}
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	wantReports := func(step string, n int) {
		t.Helper()
		if len(f.store.reports) != n {
			t.Fatalf("%s: reports = %d, want %d", step, len(f.store.reports), n)
		}
	}

	valid := flagBody(flagID, flagActorURI)
	if code := deliver("/inbox", valid, valid, reporterKey); code != http.StatusAccepted {
		t.Fatalf("signed Flag: status = %d, want %d", code, http.StatusAccepted)
	}
	wantReports("signed Flag", 1)
	report := f.store.reports[0]
	if report.AccountID != f.reporter.ID || report.TargetAccountID != f.alice.ID || report.Uri.String != flagID {
		t.Fatalf("report = %+v", report)
	}
	if len(report.StatusIds) != 1 || report.StatusIds[0] != f.alicePublic.ID || report.Comment != "Spam" {
		t.Fatalf("report = %+v", report)
	}

	// The same Flag at another inbox is not a replay of that request, so it
	// is processed again, and files nothing new.
	if code := deliver("/actor/inbox", valid, valid, reporterKey); code != http.StatusAccepted {
		t.Fatalf("redelivered Flag: status = %d, want %d", code, http.StatusAccepted)
	}
	wantReports("redelivered Flag", 1)
	if len(f.mail.mails) != 1 {
		t.Fatalf("mails = %d, want 1", len(f.mail.mails))
	}

	rejected := []struct {
		name   string
		body   []byte
		signed []byte
		key    inboxKey
	}{
		{name: "unsigned", body: flagBody(flagID+"-2", flagActorURI)},
		{name: "signed with a key that isn't the actor's", body: flagBody(flagID+"-3", flagActorURI), key: strangerKey},
		{name: "changed after signing", body: flagBody(flagID+"-4", flagActorURI), signed: flagBody(flagID+"-5", flagActorURI), key: reporterKey},
		{name: "actor that didn't sign it", body: flagBody(flagID+"-6", f.carol.Uri), key: reporterKey},
		{name: "actor's key used for another actor", body: flagBody(flagID+"-7", flagActorURI), key: carolKey},
	}
	for _, tt := range rejected {
		signed := tt.signed
		if signed == nil {
			signed = tt.body
		}
		if code := deliver("/inbox", tt.body, signed, tt.key); code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want %d", tt.name, code, http.StatusUnauthorized)
		}
		wantReports(tt.name, 1)
	}
}

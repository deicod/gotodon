package activitypub

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

type InstanceActorStore interface {
	GetAccountByUsername(ctx context.Context, username string) (db.Account, error)
	GetAccountByURI(ctx context.Context, uri string) (db.Account, error)
}

type instanceActorWriter interface {
	CreateAccount(ctx context.Context, arg db.CreateAccountParams) (db.Account, error)
	UpdateAccount(ctx context.Context, arg db.UpdateAccountParams) (db.Account, error)
}

// EnsureInstanceActor finds or creates the local instance actor account and ensures key fields are aligned.
func EnsureInstanceActor(ctx context.Context, store InstanceActorStore, domain string) (db.Account, error) {
	if store == nil {
		return db.Account{}, pgx.ErrNoRows
	}
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return db.Account{}, errors.New("instance actor domain missing")
	}

	actorURI := activitypub.InstanceActorURI(domain)
	account, err := store.GetAccountByURI(ctx, actorURI)
	if err == nil {
		return ensureInstanceActorFields(ctx, store, domain, account)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.Account{}, err
	}

	account, err = store.GetAccountByUsername(ctx, domain)
	if err == nil {
		return ensureInstanceActorFields(ctx, store, domain, account)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Account{}, err
	}

	return createInstanceActor(ctx, store, domain, actorURI)
}

func ensureInstanceActorFields(ctx context.Context, store InstanceActorStore, domain string, account db.Account) (db.Account, error) {
	writer, ok := any(store).(instanceActorWriter)
	if !ok {
		return account, nil
	}

	actorURI := activitypub.InstanceActorURI(domain)
	if actorURI == "" {
		return account, errors.New("instance actor domain missing")
	}
	actorURL := instanceActorURLForDomain(domain)

	params := db.UpdateAccountParams{ID: account.ID}
	needsUpdate := false

	if account.Uri != actorURI {
		params.Uri = pgtype.Text{String: actorURI, Valid: true}
		needsUpdate = true
	}
	if actorURL != "" && (!account.Url.Valid || account.Url.String != actorURL) {
		params.Url = pgtype.Text{String: actorURL, Valid: true}
		needsUpdate = true
	}
	if !account.ActorType.Valid || !strings.EqualFold(account.ActorType.String, activitypub.TypeApplication) {
		params.ActorType = pgtype.Text{String: activitypub.TypeApplication, Valid: true}
		needsUpdate = true
	}
	if !account.Locked {
		params.Locked = pgtype.Bool{Bool: true, Valid: true}
		needsUpdate = true
	}
	if !account.Discoverable.Valid || account.Discoverable.Bool {
		params.Discoverable = pgtype.Bool{Bool: false, Valid: true}
		needsUpdate = true
	}
	if account.Indexable {
		params.Indexable = pgtype.Bool{Bool: false, Valid: true}
		needsUpdate = true
	}

	inboxURL := actorURI + "/inbox"
	if account.InboxUrl != inboxURL {
		params.InboxUrl = pgtype.Text{String: inboxURL, Valid: true}
		needsUpdate = true
	}
	outboxURL := actorURI + "/outbox"
	if account.OutboxUrl != outboxURL {
		params.OutboxUrl = pgtype.Text{String: outboxURL, Valid: true}
		needsUpdate = true
	}
	sharedInboxURL := fmt.Sprintf("https://%s/inbox", domain)
	if account.SharedInboxUrl != sharedInboxURL {
		params.SharedInboxUrl = pgtype.Text{String: sharedInboxURL, Valid: true}
		needsUpdate = true
	}

	if !account.PrivateKey.Valid || account.PrivateKey.String == "" || account.PublicKey == "" {
		privateKey, publicKey, err := activitypub.GenerateKeyPair()
		if err != nil {
			return db.Account{}, err
		}
		publicKeyPEM, err := activitypub.EncodePublicKey(publicKey)
		if err != nil {
			return db.Account{}, err
		}
		params.PrivateKey = pgtype.Text{String: activitypub.EncodePrivateKey(privateKey), Valid: true}
		params.PublicKey = pgtype.Text{String: publicKeyPEM, Valid: true}
		needsUpdate = true
	}

	if !needsUpdate {
		return account, nil
	}

	return writer.UpdateAccount(ctx, params)
}

func createInstanceActor(ctx context.Context, store InstanceActorStore, domain, actorURI string) (db.Account, error) {
	writer, ok := any(store).(instanceActorWriter)
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	if actorURI == "" {
		return db.Account{}, errors.New("instance actor domain missing")
	}

	privateKey, publicKey, err := activitypub.GenerateKeyPair()
	if err != nil {
		return db.Account{}, err
	}
	publicKeyPEM, err := activitypub.EncodePublicKey(publicKey)
	if err != nil {
		return db.Account{}, err
	}

	actorURL := instanceActorURLForDomain(domain)
	urlValue := pgtype.Text{Valid: false}
	if actorURL != "" {
		urlValue = pgtype.Text{String: actorURL, Valid: true}
	}

	account, err := writer.CreateAccount(ctx, db.CreateAccountParams{
		Username:     domain,
		Domain:       pgtype.Text{Valid: false},
		PrivateKey:   pgtype.Text{String: activitypub.EncodePrivateKey(privateKey), Valid: true},
		PublicKey:    publicKeyPEM,
		DisplayName:  domain,
		Note:         "",
		Uri:          actorURI,
		Url:          urlValue,
		Locked:       true,
		Discoverable: pgtype.Bool{Bool: false, Valid: true},
		Indexable:    false,
		ActorType:    pgtype.Text{String: activitypub.TypeApplication, Valid: true},
		Protocol:     0,
	})
	if err != nil {
		if existing, fetchErr := store.GetAccountByURI(ctx, actorURI); fetchErr == nil {
			return existing, nil
		}
		return db.Account{}, err
	}

	return ensureInstanceActorFields(ctx, store, domain, account)
}

func instanceActorURLForDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/about/more?instance_actor=true", domain)
}

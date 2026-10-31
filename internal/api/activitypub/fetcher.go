package activitypub

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/safehttp"
)

type actorFetcher struct {
	store       Store
	localDomain string
	httpClient  *http.Client
}

// newActorFetcher creates the fetcher used to verify inbox signatures. The
// actor URI comes from an unauthenticated request, so remote actors are
// fetched only from public addresses (see safehttp).
func newActorFetcher(store Store, localDomain string) *actorFetcher {
	return &actorFetcher{
		store:       store,
		localDomain: localDomain,
		httpClient:  safehttp.NewClient(safehttp.Options{}),
	}
}

func (f *actorFetcher) FetchActor(ctx context.Context, actorURI string) (*activitypub.Actor, error) {
	if f.store != nil {
		account, err := f.store.GetAccountByURI(ctx, actorURI)
		if err == nil {
			if account.PublicKey == "" {
				if !f.isLocalActor(actorURI) {
					return f.fetchRemoteActor(ctx, actorURI)
				}
				return nil, fmt.Errorf("missing public key for actor")
			}
			// Only the PEM is persisted, so the key's id is unknown here;
			// activitypub.VerifyKeyOwner then requires the keyId to be rooted
			// at the actor URI instead of guessing "#main-key".
			return &activitypub.Actor{
				Object: activitypub.Object{
					ID:   actorURI,
					Type: activitypub.TypePerson,
				},
				PublicKey: &activitypub.PublicKey{
					Owner:        actorURI,
					PublicKeyPem: account.PublicKey,
				},
			}, nil
		}
		if f.isLocalActor(actorURI) && errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("actor not found")
		}
	}

	return f.fetchRemoteActor(ctx, actorURI)
}

func (f *actorFetcher) isLocalActor(actorURI string) bool {
	if f.localDomain == "" {
		return false
	}
	return activitypub.ExtractDomain(actorURI) == f.localDomain
}

func (f *actorFetcher) fetchRemoteActor(ctx context.Context, actorURI string) (*activitypub.Actor, error) {
	var actor activitypub.Actor
	if err := activitypub.FetchDocument(ctx, f.httpClient, actorURI, &actor); err != nil {
		return nil, fmt.Errorf("actor fetch: %w", err)
	}
	return &actor, nil
}

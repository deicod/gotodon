package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/pkg/activitypub"
)

type actorFetcher struct {
	store       Store
	localDomain string
	httpClient  *http.Client
}

func newActorFetcher(store Store, localDomain string) *actorFetcher {
	return &actorFetcher{
		store:       store,
		localDomain: localDomain,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, actorURI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", activitypub.ActivityPubContentType+", "+activitypub.ActivityPubLDContentType)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("actor fetch failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("actor fetch returned status %d", resp.StatusCode)
	}

	var actor activitypub.Actor
	if err := json.NewDecoder(resp.Body).Decode(&actor); err != nil {
		return nil, fmt.Errorf("failed to decode actor: %w", err)
	}

	return &actor, nil
}

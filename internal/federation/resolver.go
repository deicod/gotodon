package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

const defaultActorRefresh = 24 * time.Hour

// RemoteActorStore describes persistence needed for remote actor resolution.
type RemoteActorStore interface {
	GetAccountByUsername(ctx context.Context, username string) (db.Account, error)
	GetAccountByUsernameAndDomain(ctx context.Context, arg db.GetAccountByUsernameAndDomainParams) (db.Account, error)
	GetAccountByURI(ctx context.Context, uri string) (db.Account, error)
	CreateAccount(ctx context.Context, arg db.CreateAccountParams) (db.Account, error)
	CreateAccountStats(ctx context.Context, accountID int64) (db.AccountStat, error)
	UpdateAccount(ctx context.Context, arg db.UpdateAccountParams) (db.Account, error)
}

// WebFingerLookup describes WebFinger resolution.
type WebFingerLookup interface {
	Lookup(ctx context.Context, acct string) (*activitypub.WebFingerResponse, error)
}

type actorPayloadFetcher interface {
	FetchActorPayload(ctx context.Context, actorURI string) (actorPayload, error)
}

// RemoteActorResolver resolves and caches remote actors via WebFinger.
type RemoteActorResolver struct {
	store       RemoteActorStore
	localDomain string
	webfinger   WebFingerLookup
	fetcher     actorPayloadFetcher
	now         func() time.Time
	staleAfter  time.Duration
}

// NewRemoteActorResolver creates a resolver for remote actors.
func NewRemoteActorResolver(store RemoteActorStore, localDomain string) *RemoteActorResolver {
	httpClient := &http.Client{Timeout: 10 * time.Second}
	return &RemoteActorResolver{
		store:       store,
		localDomain: localDomain,
		webfinger:   activitypub.NewWebFingerClient(),
		fetcher:     &httpActorFetcher{httpClient: httpClient},
		now:         time.Now,
		staleAfter:  defaultActorRefresh,
	}
}

// ResolveAccount resolves an acct or actor URI into a cached account.
func (r *RemoteActorResolver) ResolveAccount(ctx context.Context, acct string) (db.Account, error) {
	if r.store == nil {
		return db.Account{}, errors.New("actor resolver store missing")
	}

	acct = strings.TrimSpace(acct)
	if acct == "" {
		return db.Account{}, errors.New("acct is required")
	}

	if looksLikeURL(acct) {
		return r.resolveByActorURI(ctx, acct, "", "", "")
	}

	username, domain, err := activitypub.ParseAcct(acct)
	if err != nil {
		return db.Account{}, err
	}
	if username == "" || domain == "" {
		return db.Account{}, errors.New("acct must include username and domain")
	}

	if r.localDomain != "" && strings.EqualFold(domain, r.localDomain) {
		return r.store.GetAccountByUsername(ctx, username)
	}

	var cached db.Account
	cachedFound := false
	account, err := r.store.GetAccountByUsernameAndDomain(ctx, db.GetAccountByUsernameAndDomainParams{
		Username: username,
		Domain:   domain,
	})
	if err == nil {
		cached = account
		cachedFound = true
		if !r.isStale(account) {
			return account, nil
		}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Account{}, err
	}

	if r.webfinger == nil {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("webfinger client unavailable")
	}

	wf, err := r.webfinger.Lookup(ctx, fmt.Sprintf("%s@%s", username, domain))
	if err != nil {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, err
	}
	actorURI := strings.TrimSpace(wf.GetActorURI())
	if actorURI == "" {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("webfinger response missing actor")
	}

	account, err = r.resolveByActorURI(ctx, actorURI, wf.GetProfileURL(), username, domain)
	if err != nil {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, err
	}
	return account, nil
}

func (r *RemoteActorResolver) resolveByActorURI(ctx context.Context, actorURI, profileURL, acctUsername, acctDomain string) (db.Account, error) {
	actorURI = strings.TrimSpace(actorURI)
	if actorURI == "" {
		return db.Account{}, errors.New("actor uri is required")
	}
	if r.localDomain != "" && strings.EqualFold(activitypub.ExtractDomain(actorURI), r.localDomain) {
		return r.store.GetAccountByURI(ctx, actorURI)
	}

	var cached db.Account
	cachedFound := false
	account, err := r.store.GetAccountByURI(ctx, actorURI)
	if err == nil {
		cached = account
		cachedFound = true
		if !r.isStale(account) {
			return account, nil
		}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Account{}, err
	}

	if r.fetcher == nil {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("actor fetcher unavailable")
	}

	payload, err := r.fetcher.FetchActorPayload(ctx, actorURI)
	if err != nil {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, err
	}

	if payload.ID != "" && payload.ID != actorURI {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("actor id mismatch")
	}
	if payload.ID == "" {
		payload.ID = actorURI
	}

	if payload.Type != "" && !isActorType(payload.Type) {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, fmt.Errorf("unsupported actor type %q", payload.Type)
	}
	if payload.Inbox == "" {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("actor inbox missing")
	}
	if !payload.HasPublicKey || payload.PublicKey == "" {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("actor public key missing")
	}

	domain := activitypub.ExtractDomain(actorURI)
	if domain == "" {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("actor domain missing")
	}
	if acctDomain != "" && !strings.EqualFold(domain, acctDomain) {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("actor domain mismatch")
	}

	username := payload.PreferredUsername
	if username == "" {
		username = acctUsername
	}
	if username == "" {
		username = actorUsername(nil, actorURI)
	}
	if username == "" {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("actor username missing")
	}
	if acctUsername != "" && payload.PreferredUsername != "" &&
		!strings.EqualFold(payload.PreferredUsername, acctUsername) {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, errors.New("actor username mismatch")
	}

	account, err = r.upsertAccount(ctx, cachedFound, account, payload, username, domain, actorURI, profileURL)
	if err != nil {
		if cachedFound {
			return cached, nil
		}
		return db.Account{}, err
	}
	return account, nil
}

func (r *RemoteActorResolver) upsertAccount(ctx context.Context, cachedFound bool, account db.Account, payload actorPayload, username, domain, actorURI, profileURL string) (db.Account, error) {
	displayName := payload.Name
	if displayName == "" {
		displayName = username
	}

	note := payload.Summary
	if note == "" {
		note = payload.Content
	}

	var discoverable pgtype.Bool
	if payload.Discoverable != nil {
		discoverable = pgtype.Bool{Bool: *payload.Discoverable, Valid: true}
	}

	var actorType pgtype.Text
	if payload.Type != "" {
		actorType = pgtype.Text{String: payload.Type, Valid: true}
	}

	indexable := false
	if payload.Indexable != nil {
		indexable = *payload.Indexable
	}

	locked := false
	if payload.HasManuallyApprovesFollowers {
		locked = payload.ManuallyApprovesFollowers
	}

	url := extractURL(payload.URL)
	if url == "" {
		url = profileURL
	}
	var urlValue pgtype.Text
	if url != "" {
		urlValue = pgtype.Text{String: url, Valid: true}
	}

	if !cachedFound {
		var err error
		account, err = r.store.CreateAccount(ctx, db.CreateAccountParams{
			Username:     username,
			Domain:       pgtype.Text{String: domain, Valid: true},
			PrivateKey:   pgtype.Text{Valid: false},
			PublicKey:    payload.PublicKey,
			DisplayName:  displayName,
			Note:         note,
			Uri:          actorURI,
			Url:          urlValue,
			Locked:       locked,
			Discoverable: discoverable,
			Indexable:    indexable,
			ActorType:    actorType,
			Protocol:     1,
		})
		if err != nil {
			if isUniqueViolation(err) {
				account, err = r.store.GetAccountByURI(ctx, actorURI)
				if err != nil && acctHasDomain(username, domain) {
					account, err = r.store.GetAccountByUsernameAndDomain(ctx, db.GetAccountByUsernameAndDomainParams{
						Username: username,
						Domain:   domain,
					})
				}
			}
			if err != nil {
				return db.Account{}, err
			}
		}

		if _, err := r.store.CreateAccountStats(ctx, account.ID); err != nil && !isUniqueViolation(err) {
			return db.Account{}, err
		}
	}

	params := r.updateParams(account.ID, payload, profileURL)
	account, err := r.store.UpdateAccount(ctx, params)
	if err != nil {
		return db.Account{}, err
	}
	return account, nil
}

func (r *RemoteActorResolver) updateParams(accountID int64, payload actorPayload, profileURL string) db.UpdateAccountParams {
	params := db.UpdateAccountParams{ID: accountID}

	if payload.Name != "" {
		params.DisplayName = pgtype.Text{String: payload.Name, Valid: true}
	}

	note := payload.Summary
	if note == "" {
		note = payload.Content
	}
	if payload.HasSummary || payload.HasContent {
		params.Note = pgtype.Text{String: note, Valid: true}
	} else if note != "" {
		params.Note = pgtype.Text{String: note, Valid: true}
	}

	if payload.HasManuallyApprovesFollowers {
		params.Locked = pgtype.Bool{Bool: payload.ManuallyApprovesFollowers, Valid: true}
	}
	if payload.Discoverable != nil {
		params.Discoverable = pgtype.Bool{Bool: *payload.Discoverable, Valid: true}
	}
	if payload.Indexable != nil {
		params.Indexable = pgtype.Bool{Bool: *payload.Indexable, Valid: true}
	}

	if url := extractURL(payload.URL); url != "" {
		params.Url = pgtype.Text{String: url, Valid: true}
	} else if profileURL != "" {
		params.Url = pgtype.Text{String: profileURL, Valid: true}
	}

	if payload.HasPublicKey {
		params.PublicKey = pgtype.Text{String: payload.PublicKey, Valid: true}
	}
	if payload.Type != "" {
		params.ActorType = pgtype.Text{String: payload.Type, Valid: true}
	}
	if payload.Inbox != "" {
		params.InboxUrl = pgtype.Text{String: payload.Inbox, Valid: true}
	}
	if payload.Outbox != "" {
		params.OutboxUrl = pgtype.Text{String: payload.Outbox, Valid: true}
	}
	if payload.Followers != "" {
		params.FollowersUrl = pgtype.Text{String: payload.Followers, Valid: true}
	}
	if payload.Following != "" {
		params.FollowingUrl = pgtype.Text{String: payload.Following, Valid: true}
	}
	if payload.Featured != "" {
		params.FeaturedCollectionUrl = pgtype.Text{String: payload.Featured, Valid: true}
	}
	if payload.SharedInbox != "" {
		params.SharedInboxUrl = pgtype.Text{String: payload.SharedInbox, Valid: true}
	}

	if avatarURL := imageURL(payload.Icon); avatarURL != "" {
		params.AvatarRemoteUrl = pgtype.Text{String: avatarURL, Valid: true}
	}
	if headerURL := imageURL(payload.Image); headerURL != "" {
		params.HeaderRemoteUrl = pgtype.Text{String: headerURL, Valid: true}
	}

	if payload.HasAttachment {
		fields := parseActorFields(payload.Attachment)
		if data, err := json.Marshal(fields); err == nil {
			params.Fields = data
		}
	}

	params.LastWebfingeredAt = pgtype.Timestamptz{Time: r.nowTime().UTC(), Valid: true}

	return params
}

func (r *RemoteActorResolver) isStale(account db.Account) bool {
	if !account.LastWebfingeredAt.Valid {
		return true
	}
	staleAfter := r.staleAfter
	if staleAfter <= 0 {
		staleAfter = defaultActorRefresh
	}
	return r.nowTime().Sub(account.LastWebfingeredAt.Time) > staleAfter
}

func (r *RemoteActorResolver) nowTime() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

type httpActorFetcher struct {
	httpClient *http.Client
}

func (f *httpActorFetcher) FetchActorPayload(ctx context.Context, actorURI string) (actorPayload, error) {
	if f.httpClient == nil {
		f.httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, actorURI, nil)
	if err != nil {
		return actorPayload{}, err
	}
	req.Header.Set("Accept", activitypub.ActivityPubContentType+", "+activitypub.ActivityPubLDContentType)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return actorPayload{}, fmt.Errorf("actor fetch failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return actorPayload{}, fmt.Errorf("actor fetch returned status %d", resp.StatusCode)
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return actorPayload{}, fmt.Errorf("failed to decode actor: %w", err)
	}
	if len(raw) == 0 {
		return actorPayload{}, errors.New("actor payload empty")
	}

	return actorPayloadFromMap(raw), nil
}

func looksLikeURL(value string) bool {
	return strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://")
}

func acctHasDomain(username, domain string) bool {
	return username != "" && domain != ""
}

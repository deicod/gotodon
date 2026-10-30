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

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// AnnounceStore describes the persistence needed for incoming Announce activities.
type AnnounceStore interface {
	CreateStore
	GetReblogByAccountAndOriginal(ctx context.Context, arg db.GetReblogByAccountAndOriginalParams) (db.Status, error)
	IncrementStatusReblogs(ctx context.Context, statusID int64) error
}

// ObjectFetcher retrieves remote ActivityPub objects.
type ObjectFetcher interface {
	FetchObject(ctx context.Context, objectURI string) (any, error)
}

// AnnounceService processes incoming Announce activities (boosts).
type AnnounceService struct {
	store         AnnounceStore
	localDomain   string
	fetcher       activitypub.ActorFetcher
	objectFetcher ObjectFetcher
	helper        *CreateService
	pushPublisher PushNotificationPublisher
}

// SetPushPublisher enables Web Push delivery for reblog notifications.
func (s *AnnounceService) SetPushPublisher(publisher PushNotificationPublisher) {
	s.pushPublisher = publisher
}

// NewAnnounceService creates a service for Announce activities.
func NewAnnounceService(store AnnounceStore, localDomain string, fetcher activitypub.ActorFetcher, objectFetcher ObjectFetcher) *AnnounceService {
	if objectFetcher == nil {
		objectFetcher = newHTTPObjectFetcher()
	}
	return &AnnounceService{
		store:         store,
		localDomain:   localDomain,
		fetcher:       fetcher,
		objectFetcher: objectFetcher,
		helper:        NewCreateService(store, localDomain, fetcher),
	}
}

// HandleAnnounce processes an Announce activity and creates a reblog.
func (s *AnnounceService) HandleAnnounce(ctx context.Context, activity *activitypub.Activity) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid announce activity"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing announce actor"}
	}
	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}
	if isLocalURI(actorURI, s.localDomain) {
		return nil
	}

	objectValue := unwrapAnnounceObject(activity.ActivityObject)
	objectURI := extractObjectURI(objectValue)
	if objectURI == "" {
		objectURI = noteURIFromObject(objectValue)
	}
	if objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing announce object"}
	}

	announcer, err := s.ensureAnnouncerAccount(ctx, actorURI)
	if err != nil {
		return err
	}

	original, err := s.resolveOriginalStatus(ctx, objectValue, objectURI)
	if err != nil {
		return err
	}
	if original.ID == 0 {
		return nil
	}
	if original.Visibility == httpVisibilityPrivate || original.Visibility == httpVisibilityDirect {
		return nil
	}

	created, err := s.ensureReblog(ctx, activity, announcer, original)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}

	return s.notifyReblog(ctx, announcer, original)
}

func (s *AnnounceService) ensureAnnouncerAccount(ctx context.Context, actorURI string) (db.Account, error) {
	if s.fetcher == nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusInternalError, Message: "Actor fetcher unavailable"}
	}
	actor, err := s.fetcher.FetchActor(ctx, actorURI)
	if err != nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor not found"}
	}
	account, err := s.helper.ensureActorAccount(ctx, actorURI, actor)
	if err != nil {
		return db.Account{}, err
	}
	if account.SuspendedAt.Valid {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusForbidden, Message: "Actor is suspended"}
	}
	return account, nil
}

func (s *AnnounceService) resolveOriginalStatus(ctx context.Context, objectValue any, objectURI string) (db.Status, error) {
	status, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: objectURI, Valid: true})
	if err == nil {
		return status, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Status{}, err
	}

	if isLocalURI(objectURI, s.localDomain) {
		return db.Status{}, nil
	}

	status, err = s.createStatusFromObject(ctx, objectValue, objectURI)
	if err != nil || status.ID != 0 {
		return status, err
	}

	if s.objectFetcher == nil {
		return db.Status{}, nil
	}
	remoteObject, err := s.objectFetcher.FetchObject(ctx, objectURI)
	if err != nil {
		return db.Status{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Announce object not found"}
	}

	return s.createStatusFromObject(ctx, remoteObject, objectURI)
}

func (s *AnnounceService) createStatusFromObject(ctx context.Context, objectValue any, objectURI string) (db.Status, error) {
	noteValue := unwrapAnnounceObject(objectValue)
	if noteValue == nil {
		return db.Status{}, nil
	}
	if _, ok := noteValue.(string); ok {
		return db.Status{}, nil
	}

	note, err := parseCreateNote(noteValue)
	if err != nil {
		return db.Status{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: err.Error()}
	}
	if note.Type == "" {
		return db.Status{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing note type"}
	}
	if !strings.EqualFold(note.Type, activitypub.TypeNote) {
		return db.Status{}, nil
	}

	noteValue, noteURI := ensureNoteID(note, noteValue, objectURI)
	if noteURI == "" {
		return db.Status{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing note id"}
	}

	authorURI := extractAttributedActor(note.AttributedTo)
	if authorURI == "" {
		return db.Status{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing note actor"}
	}

	createActivity := &activitypub.Activity{
		Type:           activitypub.TypeCreate,
		Actor:          authorURI,
		To:             note.To,
		Cc:             note.Cc,
		ActivityObject: noteValue,
	}

	if err := s.helper.HandleCreate(ctx, createActivity); err != nil {
		return db.Status{}, err
	}

	status, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: noteURI, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Status{}, nil
		}
		return db.Status{}, err
	}
	return status, nil
}

func (s *AnnounceService) ensureReblog(ctx context.Context, activity *activitypub.Activity, announcer db.Account, original db.Status) (bool, error) {
	if activity.ID != "" {
		if _, err := s.store.GetStatusByURI(ctx, pgtype.Text{String: activity.ID, Valid: true}); err == nil {
			return false, nil
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}

	_, err := s.store.GetReblogByAccountAndOriginal(ctx, db.GetReblogByAccountAndOriginalParams{
		AccountID:  announcer.ID,
		ReblogOfID: pgtype.Int8{Int64: original.ID, Valid: true},
	})
	if err == nil {
		return false, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}

	uri := pgtype.Text{Valid: false}
	if activity.ID != "" {
		uri = pgtype.Text{String: activity.ID, Valid: true}
	}

	reblog, err := s.store.CreateStatus(ctx, db.CreateStatusParams{
		Uri:        uri,
		AccountID:  announcer.ID,
		ReblogOfID: pgtype.Int8{Int64: original.ID, Valid: true},
		Visibility: original.Visibility,
		Local:      pgtype.Bool{Bool: false, Valid: true},
	})
	if err != nil {
		// The announce is already known: a concurrent delivery stored it
		// first, or it was undone and its URI is kept to stop it from returning.
		if isUniqueViolation(err) {
			return false, nil
		}
		return false, err
	}

	if published, ok := parseTimestamp(activity.Published); ok {
		_ = s.store.UpdateStatusTimestamps(ctx, db.UpdateStatusTimestampsParams{
			ID:        reblog.ID,
			CreatedAt: published,
			UpdatedAt: published,
		})
	}

	if _, err := s.store.CreateStatusStats(ctx, reblog.ID); err != nil && !isUniqueViolation(err) {
		return false, err
	}
	if _, err := s.store.CreateStatusStats(ctx, original.ID); err != nil && !isUniqueViolation(err) {
		return false, err
	}

	if err := s.store.IncrementStatusReblogs(ctx, original.ID); err != nil {
		return false, err
	}
	return true, nil
}

func (s *AnnounceService) notifyReblog(ctx context.Context, announcer db.Account, original db.Status) error {
	if !original.Local.Valid || !original.Local.Bool {
		return nil
	}
	if original.AccountID == announcer.ID {
		return nil
	}

	suppressed, err := s.isNotificationSuppressed(ctx, original.AccountID, announcer.ID)
	if err != nil || suppressed {
		return err
	}

	notifs, err := s.store.CreateNotifications(ctx, db.CreateNotificationsParams{
		Column1:       []int64{original.AccountID},
		FromAccountID: announcer.ID,
		ActivityType:  "Status",
		ActivityID:    original.ID,
		Type:          pgtype.Text{String: "reblog", Valid: true},
	})
	if err != nil {
		return err
	}
	publishPushNotifications(ctx, s.pushPublisher, notifs)
	return nil
}

func (s *AnnounceService) isNotificationSuppressed(ctx context.Context, accountID, announcerID int64) (bool, error) {
	mutes, err := s.store.GetMutesFromAccounts(ctx, db.GetMutesFromAccountsParams{
		TargetAccountID: announcerID,
		AccountIds:      []int64{accountID},
	})
	if err != nil {
		return false, err
	}
	if len(mutes) > 0 {
		return true, nil
	}

	blocks, err := s.store.GetBlocksFromAccounts(ctx, db.GetBlocksFromAccountsParams{
		TargetAccountID: announcerID,
		AccountIds:      []int64{accountID},
	})
	if err != nil {
		return false, err
	}
	return len(blocks) > 0, nil
}

func unwrapAnnounceObject(value any) any {
	switch obj := value.(type) {
	case activitypub.Activity:
		return obj.ActivityObject
	case *activitypub.Activity:
		if obj != nil {
			return obj.ActivityObject
		}
	case map[string]any:
		typ := coerceActivityType(obj["type"])
		if obj["object"] != nil && !strings.EqualFold(typ, activitypub.TypeNote) {
			return obj["object"]
		}
	}
	return value
}

func ensureNoteID(note *notePayload, noteValue any, fallback string) (any, string) {
	if note == nil {
		return noteValue, ""
	}

	noteURI := note.ID
	if noteURI == "" {
		noteURI = extractURL(note.URL)
	}
	if noteURI != "" {
		return noteValue, noteURI
	}
	if fallback == "" {
		return noteValue, ""
	}

	switch v := noteValue.(type) {
	case map[string]any:
		v["id"] = fallback
		return v, fallback
	case activitypub.Object:
		v.ID = fallback
		return v, fallback
	case *activitypub.Object:
		if v != nil {
			v.ID = fallback
			return v, fallback
		}
	}

	return noteValue, ""
}

func noteURIFromObject(value any) string {
	noteValue := unwrapAnnounceObject(value)
	if noteValue == nil {
		return ""
	}
	if _, ok := noteValue.(string); ok {
		return ""
	}

	note, err := parseCreateNote(noteValue)
	if err != nil {
		return ""
	}
	if note.Type == "" || !strings.EqualFold(note.Type, activitypub.TypeNote) {
		return ""
	}

	if note.ID != "" {
		return note.ID
	}
	return extractURL(note.URL)
}

type httpObjectFetcher struct {
	client *http.Client
}

func newHTTPObjectFetcher() *httpObjectFetcher {
	return &httpObjectFetcher{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (f *httpObjectFetcher) FetchObject(ctx context.Context, objectURI string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, objectURI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", activitypub.ActivityPubContentType+", "+activitypub.ActivityPubLDContentType)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("object fetch failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("object fetch returned status %d", resp.StatusCode)
	}

	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("failed to decode object: %w", err)
	}

	return payload, nil
}

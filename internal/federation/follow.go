package federation

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	apihandler "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// FollowStore describes the persistence needed to handle incoming follows.
type FollowStore interface {
	GetAccountByURI(ctx context.Context, uri string) (db.Account, error)
	CreateAccount(ctx context.Context, arg db.CreateAccountParams) (db.Account, error)
	CreateAccountStats(ctx context.Context, accountID int64) (db.AccountStat, error)
	GetFollow(ctx context.Context, arg db.GetFollowParams) (db.Follow, error)
	GetFollowRequest(ctx context.Context, arg db.GetFollowRequestParams) (db.FollowRequest, error)
	CreateFollow(ctx context.Context, arg db.CreateFollowParams) (db.Follow, error)
	CreateFollowRequest(ctx context.Context, arg db.CreateFollowRequestParams) (db.FollowRequest, error)
	DeleteFollowRequest(ctx context.Context, arg db.DeleteFollowRequestParams) error
	GetBlock(ctx context.Context, arg db.GetBlockParams) (db.Block, error)
}

// ActivityDelivery sends a signed ActivityPub activity to a remote inbox.
type ActivityDelivery func(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error

// FollowService processes incoming Follow activities.
type FollowService struct {
	store       FollowStore
	fetcher     activitypub.ActorFetcher
	localDomain string
	deliver     ActivityDelivery
}

// NewFollowService creates a follow service with optional delivery override.
func NewFollowService(store FollowStore, localDomain string, fetcher activitypub.ActorFetcher, delivery ActivityDelivery) *FollowService {
	if delivery == nil {
		delivery = defaultDelivery
	}
	return &FollowService{
		store:       store,
		fetcher:     fetcher,
		localDomain: localDomain,
		deliver:     delivery,
	}
}

// HandleFollow validates and processes an incoming Follow activity.
func (s *FollowService) HandleFollow(ctx context.Context, activity *activitypub.Activity) error {
	if activity == nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Invalid follow activity"}
	}

	actorURI := extractActorURI(activity.Actor)
	if actorURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing follow actor"}
	}

	objectURI := extractObjectURI(activity.ActivityObject)
	if objectURI == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Missing follow object"}
	}

	if s.localDomain == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local domain not configured"}
	}
	if !isLocalURI(objectURI, s.localDomain) {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Follow target is not local"}
	}

	targetAccount, err := s.store.GetAccountByURI(ctx, objectURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &apihandler.InboxError{Status: httpStatusNotFound, Message: "Follow target not found"}
		}
		return err
	}
	if targetAccount.Domain.Valid {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Follow target is not local"}
	}
	if targetAccount.SuspendedAt.Valid {
		return &apihandler.InboxError{Status: httpStatusForbidden, Message: "Follow target is suspended"}
	}

	actor, err := s.fetcher.FetchActor(ctx, actorURI)
	if err != nil {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor not found"}
	}
	if actor.PublicKey == nil || actor.PublicKey.PublicKeyPem == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor missing public key"}
	}

	followerAccount, err := s.ensureActorAccount(ctx, actorURI, actor)
	if err != nil {
		return err
	}
	if followerAccount.ID == targetAccount.ID {
		return &apihandler.InboxError{Status: httpStatusUnprocessable, Message: "Cannot follow self"}
	}

	blocked, err := s.isBlocked(ctx, targetAccount.ID, followerAccount.ID)
	if err != nil {
		return err
	}
	if blocked {
		return s.sendFollowResponse(ctx, targetAccount, actor, activity, activitypub.TypeReject)
	}

	_, err = s.store.GetFollow(ctx, db.GetFollowParams{
		AccountID:       followerAccount.ID,
		TargetAccountID: targetAccount.ID,
	})
	if err == nil {
		return s.sendFollowResponse(ctx, targetAccount, actor, activity, activitypub.TypeAccept)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	uri := pgtype.Text{Valid: false}
	if activity.ID != "" {
		uri = pgtype.Text{String: activity.ID, Valid: true}
	}

	if targetAccount.Locked {
		if err := s.ensureFollowRequest(ctx, followerAccount.ID, targetAccount.ID, uri); err != nil {
			return err
		}
		return nil
	}

	if err := s.ensureFollow(ctx, followerAccount.ID, targetAccount.ID, uri); err != nil {
		return err
	}
	_ = s.store.DeleteFollowRequest(ctx, db.DeleteFollowRequestParams{
		AccountID:       followerAccount.ID,
		TargetAccountID: targetAccount.ID,
	})

	return s.sendFollowResponse(ctx, targetAccount, actor, activity, activitypub.TypeAccept)
}

func (s *FollowService) ensureFollow(ctx context.Context, followerID, targetID int64, uri pgtype.Text) error {
	_, err := s.store.CreateFollow(ctx, db.CreateFollowParams{
		AccountID:       followerID,
		TargetAccountID: targetID,
		ShowReblogs:     true,
		Notify:          false,
		Uri:             uri,
	})
	if err != nil && !isUniqueViolation(err) {
		return err
	}
	return nil
}

func (s *FollowService) ensureFollowRequest(ctx context.Context, followerID, targetID int64, uri pgtype.Text) error {
	_, err := s.store.GetFollowRequest(ctx, db.GetFollowRequestParams{
		AccountID:       followerID,
		TargetAccountID: targetID,
	})
	if err == nil {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	_, err = s.store.CreateFollowRequest(ctx, db.CreateFollowRequestParams{
		AccountID:       followerID,
		TargetAccountID: targetID,
		ShowReblogs:     true,
		Notify:          false,
		Uri:             uri,
	})
	if err != nil && !isUniqueViolation(err) {
		return err
	}
	return nil
}

func (s *FollowService) sendFollowResponse(ctx context.Context, localAccount db.Account, actor *activitypub.Actor, follow *activitypub.Activity, responseType string) error {
	inbox := actorInbox(actor)
	if inbox == "" {
		return &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor inbox missing"}
	}
	if localAccount.Uri == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local actor URI missing"}
	}
	if !localAccount.PrivateKey.Valid || localAccount.PrivateKey.String == "" {
		return &apihandler.InboxError{Status: httpStatusInternalError, Message: "Local actor signing key missing"}
	}

	response := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           responseType,
		Actor:          localAccount.Uri,
		ActivityObject: follow,
	}

	if err := s.deliver(ctx, localAccount, inbox, response); err != nil {
		return &apihandler.InboxError{Status: httpStatusBadGateway, Message: "Failed to deliver follow response"}
	}
	return nil
}

func (s *FollowService) ensureActorAccount(ctx context.Context, actorURI string, actor *activitypub.Actor) (db.Account, error) {
	account, err := s.store.GetAccountByURI(ctx, actorURI)
	if err == nil {
		return account, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Account{}, err
	}

	if s.isLocalActor(actorURI) {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusNotFound, Message: "Actor not found"}
	}
	if actor == nil {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor not found"}
	}
	if actor.PublicKey == nil || actor.PublicKey.PublicKeyPem == "" {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor missing public key"}
	}

	username := actorUsername(actor, actorURI)
	if username == "" {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor missing username"}
	}

	domain := activitypub.ExtractDomain(actorURI)
	if domain == "" {
		return db.Account{}, &apihandler.InboxError{Status: httpStatusBadRequest, Message: "Actor domain invalid"}
	}

	displayName := actor.Name
	if displayName == "" {
		displayName = username
	}
	note := actor.Summary
	if note == "" {
		note = actor.Content
	}

	var discoverable pgtype.Bool
	if actor.Discoverable != nil {
		discoverable = pgtype.Bool{Bool: *actor.Discoverable, Valid: true}
	}

	var actorType pgtype.Text
	if actor.Type != "" {
		actorType = pgtype.Text{String: actor.Type, Valid: true}
	}

	indexable := false
	if actor.Indexable != nil {
		indexable = *actor.Indexable
	}

	account, err = s.store.CreateAccount(ctx, db.CreateAccountParams{
		Username:     username,
		Domain:       pgtype.Text{String: domain, Valid: true},
		PrivateKey:   pgtype.Text{Valid: false},
		PublicKey:    actor.PublicKey.PublicKeyPem,
		DisplayName:  displayName,
		Note:         note,
		Uri:          actorURI,
		Url:          actorURLValue(actor),
		Locked:       actor.ManuallyApprovesFollowers,
		Discoverable: discoverable,
		Indexable:    indexable,
		ActorType:    actorType,
		Protocol:     1,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return s.store.GetAccountByURI(ctx, actorURI)
		}
		return db.Account{}, err
	}

	if _, err := s.store.CreateAccountStats(ctx, account.ID); err != nil && !isUniqueViolation(err) {
		return db.Account{}, err
	}

	return account, nil
}

func (s *FollowService) isBlocked(ctx context.Context, targetID, actorID int64) (bool, error) {
	_, err := s.store.GetBlock(ctx, db.GetBlockParams{
		AccountID:       targetID,
		TargetAccountID: actorID,
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (s *FollowService) isLocalActor(uri string) bool {
	return isLocalURI(uri, s.localDomain)
}

func isLocalURI(uri, localDomain string) bool {
	if uri == "" || localDomain == "" {
		return false
	}
	return strings.EqualFold(activitypub.ExtractDomain(uri), localDomain)
}

func extractActorURI(actor any) string {
	switch value := actor.(type) {
	case string:
		return value
	case map[string]any:
		if id, ok := value["id"].(string); ok {
			return id
		}
	}
	return ""
}

func extractObjectURI(object any) string {
	switch value := object.(type) {
	case string:
		return value
	case map[string]any:
		if id, ok := value["id"].(string); ok {
			return id
		}
	case activitypub.Object:
		return value.ID
	}
	return ""
}

func actorUsername(actor *activitypub.Actor, actorURI string) string {
	if actor != nil && actor.PreferredUsername != "" {
		return actor.PreferredUsername
	}
	if actorURI == "" {
		return ""
	}
	parsed, err := url.Parse(actorURI)
	if err != nil {
		return ""
	}
	base := path.Base(parsed.Path)
	if base == "." || base == "/" {
		return ""
	}
	return base
}

func actorURLValue(actor *activitypub.Actor) pgtype.Text {
	if actor == nil {
		return pgtype.Text{Valid: false}
	}
	switch value := actor.URL.(type) {
	case string:
		if value != "" {
			return pgtype.Text{String: value, Valid: true}
		}
	case map[string]any:
		if href, ok := value["href"].(string); ok && href != "" {
			return pgtype.Text{String: href, Valid: true}
		}
	}
	return pgtype.Text{Valid: false}
}

func actorInbox(actor *activitypub.Actor) string {
	if actor == nil {
		return ""
	}
	if actor.Inbox != "" {
		return actor.Inbox
	}
	if actor.Endpoints != nil && actor.Endpoints.SharedInbox != "" {
		return actor.Endpoints.SharedInbox
	}
	return ""
}

func defaultDelivery(ctx context.Context, localActor db.Account, inbox string, activity *activitypub.Activity) error {
	return NewDeliveryService().DeliverActivity(ctx, localActor, inbox, activity)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

const (
	httpStatusBadRequest    = 400
	httpStatusForbidden     = 403
	httpStatusNotFound      = 404
	httpStatusUnprocessable = 422
	httpStatusInternalError = 500
	httpStatusBadGateway    = 502
)

package federation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// ErrInboxMissing is returned when an activity has no inbox to go to.
var ErrInboxMissing = errors.New("federation: inbox missing")

// errAccountNotSuspended is returned when a suspension is delivered for an
// account that isn't suspended.
var errAccountNotSuspended = errors.New("federation: account is not suspended")

// FollowRejection names a follow, or a pending follow request, that a remote
// account has of a local account and that the local account rejects.
type FollowRejection struct {
	// Follower is the remote account that follows or asked to follow.
	Follower db.Account
	// Target is the local account that is followed.
	Target db.Account
	// ID is the follow's or the follow request's ID.
	ID int64
	// URI is the ID of the Follow activity that created it.
	URI pgtype.Text
	// Request is true for a pending follow request, false for a follow.
	Request bool
}

// DeliverAccountSuspension sends the remote followers of account, a
// suspended local account, an Update of its actor marked suspended, signed
// by the account itself, as Mastodon's
// SuspendAccountService#distribute_update_actor! does. The actor carries
// what ActivityPub::ActorSerializer keeps of a suspended account: its URIs
// and key, its username as its name, and nothing of its profile. The
// activity ID is the actor's URI and its updated_at, as
// ActivityPub::UpdateSerializer builds it, so a retry sends the same
// activity. A remote account gets nothing: its own server federates it.
func (s *OutgoingService) DeliverAccountSuspension(ctx context.Context, account db.Account) error {
	if !accountIsLocal(account, s.localDomain) {
		return nil
	}
	if !account.SuspendedAt.Valid {
		return errAccountNotSuspended
	}
	localActor, actor, err := ensureLocalActor(s.localDomain, account)
	if err != nil {
		return err
	}

	updatedAt := account.UpdatedAt
	if !updatedAt.Valid {
		updatedAt = account.SuspendedAt
	}
	activity := &activitypub.Activity{
		Context:        activitypub.MastodonContextFull(),
		Type:           activitypub.TypeUpdate,
		ID:             fmt.Sprintf("%s#updates/%d", actor, updatedAt.Time.Unix()),
		Actor:          actor,
		Published:      formatTimestamp(updatedAt.Time),
		To:             []string{activitypub.PublicAudience},
		ActivityObject: s.suspendedActor(account, actor),
	}

	followers, err := s.resolver.ResolveFollowerInboxes(ctx, account.ID)
	if err != nil {
		return err
	}
	inboxes := make([]string, 0, len(followers))
	for _, follower := range followers {
		inboxes = append(inboxes, follower.Inbox)
	}
	return s.enqueueTargets(localActor, activity, inboxes)
}

// suspendedActor is ActivityPub::ActorSerializer of account, a suspended
// local account whose actor URI is actor.
func (s *OutgoingService) suspendedActor(account db.Account, actor string) activitypub.Actor {
	disabled := false
	suspended := activitypub.Actor{
		Object: activitypub.Object{
			ID:   actor,
			Type: activitypub.TypePerson,
			Name: account.Username,
			URL:  fmt.Sprintf("https://%s/@%s", s.localDomain, account.Username),
		},
		PreferredUsername: account.Username,
		Inbox:             actor + "/inbox",
		Outbox:            actor + "/outbox",
		Followers:         actor + "/followers",
		Following:         actor + "/following",
		Featured:          actor + "/collections/featured",
		FeaturedTags:      actor + "/collections/tags",
		Discoverable:      &disabled,
		Indexable:         &disabled,
		Memorial:          account.Memorial,
		Suspended:         true,
		Endpoints: &activitypub.Endpoints{
			SharedInbox: fmt.Sprintf("https://%s/inbox", s.localDomain),
		},
	}
	if account.PublicKey != "" {
		suspended.PublicKey = &activitypub.PublicKey{
			ID:           actor + "#main-key",
			Owner:        actor,
			PublicKeyPem: account.PublicKey,
		}
	}
	if account.CreatedAt.Valid {
		suspended.Published = formatTimestamp(account.CreatedAt.Time)
	}
	return suspended
}

// DeliverFollowReject sends the remote follower of rejection a Reject of its
// Follow of the local target, signed by the target, to the follower's own
// inbox, as Mastodon's SuspendAccountService#reject_remote_follows! does
// with ActivityPub::RejectFollowSerializer. The accounts are given, not
// looked up, because a suspended account isn't found by GetAccountByID. The
// activity ID is fixed by the follow, so a retry sends the same activity. A
// follower with no inbox gets ErrInboxMissing.
func (s *OutgoingService) DeliverFollowReject(ctx context.Context, rejection FollowRejection) error {
	follower, target := rejection.Follower, rejection.Target
	if accountIsLocal(follower, s.localDomain) || !accountIsLocal(target, s.localDomain) {
		return nil
	}
	inbox := follower.InboxUrl
	if inbox == "" {
		inbox = accountInbox(follower)
	}
	if inbox == "" {
		return ErrInboxMissing
	}
	localActor, actor, err := ensureLocalActor(s.localDomain, target)
	if err != nil {
		return err
	}

	followID := rejection.URI.String
	if !rejection.URI.Valid || followID == "" {
		followID = followActivityID(s.localDomain, rejection.ID)
	}
	kind := "follows"
	if rejection.Request {
		kind = "follow_requests"
	}
	activity := &activitypub.Activity{
		Context: activitypub.DefaultContext(),
		Type:    activitypub.TypeReject,
		ID:      fmt.Sprintf("%s#rejects/%s/%d", actor, kind, rejection.ID),
		Actor:   actor,
		ActivityObject: &activitypub.Activity{
			Type:           activitypub.TypeFollow,
			ID:             followID,
			Actor:          follower.Uri,
			ActivityObject: actor,
		},
	}
	return s.enqueueTargets(localActor, activity, []string{inbox})
}

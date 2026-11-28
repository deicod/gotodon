package federation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// FollowUndo names a follow that a local account has of a remote account
// and that ends because the remote account is deleted.
type FollowUndo struct {
	// Follower is the local account that follows.
	Follower db.Account
	// Target is the remote account that is followed.
	Target db.Account
	// ID is the follow's ID.
	ID int64
	// URI is the ID of the Follow activity that created it.
	URI pgtype.Text
}

// DeliverAccountDeletion sends a Delete of the actor of account, a local
// account whose deletion has begun, signed by the account itself, as
// Mastodon's DeleteAccountService#delete_actor! does with
// ActivityPub::DeleteActorSerializer. It goes first to the inboxes of the
// account's followers, then to every other inbox in inboxes, which is meant
// to hold every inbox the instance knows (Account.inboxes), so that each
// server that may have cached the account deletes it. Each inbox gets it
// once. The activity ID is the actor's URI and "#delete", so a retry sends
// the same activity. A remote account gets nothing: its own server
// federates it.
func (s *OutgoingService) DeliverAccountDeletion(ctx context.Context, account db.Account, inboxes []string) error {
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

	activity := &activitypub.Activity{
		Context:        activitypub.DefaultContext(),
		Type:           activitypub.TypeDelete,
		ID:             actor + "#delete",
		Actor:          actor,
		To:             []string{activitypub.PublicAudience},
		ActivityObject: actor,
	}

	followers, err := s.resolver.ResolveFollowerInboxes(ctx, account.ID)
	if err != nil {
		return err
	}
	targets := make([]string, 0, len(followers)+len(inboxes))
	seen := make(map[string]bool, len(followers)+len(inboxes))
	for _, follower := range followers {
		if !seen[follower.Inbox] {
			seen[follower.Inbox] = true
			targets = append(targets, follower.Inbox)
		}
	}
	for _, inbox := range inboxes {
		if inbox != "" && !seen[inbox] {
			seen[inbox] = true
			targets = append(targets, inbox)
		}
	}
	return s.enqueueTargets(localActor, activity, targets)
}

// DeliverFollowUndo sends the remote target of undo an Undo of the Follow
// that the local follower has of it, signed by the follower, to the
// target's own inbox, as Mastodon's DeleteAccountService#undo_follows! does
// with ActivityPub::UndoFollowSerializer when it deletes a remote account:
// the target's server learns that the follow is over. The accounts are
// given, not looked up, because a suspended account isn't found by
// GetAccountByID. The activity ID is fixed by the follow, so a retry sends
// the same activity. A target with no inbox gets ErrInboxMissing.
func (s *OutgoingService) DeliverFollowUndo(ctx context.Context, undo FollowUndo) error {
	follower, target := undo.Follower, undo.Target
	if !accountIsLocal(follower, s.localDomain) || accountIsLocal(target, s.localDomain) {
		return nil
	}
	inbox := target.InboxUrl
	if inbox == "" {
		inbox = accountInbox(target)
	}
	if inbox == "" {
		return ErrInboxMissing
	}
	localActor, actor, err := ensureLocalActor(s.localDomain, follower)
	if err != nil {
		return err
	}

	followID := undo.URI.String
	if !undo.URI.Valid || followID == "" {
		followID = followActivityID(s.localDomain, undo.ID)
	}
	activity := &activitypub.Activity{
		Context: activitypub.DefaultContext(),
		Type:    activitypub.TypeUndo,
		ID:      fmt.Sprintf("%s#follows/%d/undo", actor, undo.ID),
		Actor:   actor,
		ActivityObject: &activitypub.Activity{
			Type:           activitypub.TypeFollow,
			ID:             followID,
			Actor:          actor,
			ActivityObject: target.Uri,
		},
	}
	return s.enqueueTargets(localActor, activity, []string{inbox})
}

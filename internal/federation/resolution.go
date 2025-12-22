package federation

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/deicod/gotodon/internal/db"
)

const defaultFollowerBatchSize int32 = 200

// InboxResolutionStore describes the data needed to resolve follower inboxes.
type InboxResolutionStore interface {
	GetFollowers(ctx context.Context, arg db.GetFollowersParams) ([]db.Account, error)
}

// InboxTarget groups follower accounts by inbox URL.
type InboxTarget struct {
	Inbox      string
	AccountIDs []int64
}

// InboxResolver resolves and batches remote follower inboxes.
type InboxResolver struct {
	store       InboxResolutionStore
	localDomain string
	batchSize   int32
}

// NewInboxResolver creates a follower inbox resolver.
func NewInboxResolver(store InboxResolutionStore, localDomain string) *InboxResolver {
	return &InboxResolver{
		store:       store,
		localDomain: localDomain,
		batchSize:   defaultFollowerBatchSize,
	}
}

// ResolveFollowerInboxes returns unique inbox targets for a local account's followers.
func (r *InboxResolver) ResolveFollowerInboxes(ctx context.Context, accountID int64) ([]InboxTarget, error) {
	if r.store == nil {
		return nil, errors.New("inbox resolver store missing")
	}
	limit := r.batchSize
	if limit <= 0 {
		limit = defaultFollowerBatchSize
	}

	inboxes := make(map[string]*InboxTarget)
	offset := int32(0)

	for {
		followers, err := r.store.GetFollowers(ctx, db.GetFollowersParams{
			TargetAccountID: accountID,
			Limit:           limit,
			Offset:          offset,
		})
		if err != nil {
			return nil, err
		}
		if len(followers) == 0 {
			break
		}

		for _, follower := range followers {
			if !follower.Domain.Valid || follower.Domain.String == "" {
				continue
			}
			if r.localDomain != "" && strings.EqualFold(follower.Domain.String, r.localDomain) {
				continue
			}

			inbox := follower.SharedInboxUrl
			if inbox == "" {
				inbox = follower.InboxUrl
			}
			if inbox == "" {
				continue
			}

			target := inboxes[inbox]
			if target == nil {
				target = &InboxTarget{Inbox: inbox}
				inboxes[inbox] = target
			}
			target.AccountIDs = append(target.AccountIDs, follower.ID)
		}

		offset += int32(len(followers))
		if int32(len(followers)) < limit {
			break
		}
	}

	keys := make([]string, 0, len(inboxes))
	for key := range inboxes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	targets := make([]InboxTarget, 0, len(keys))
	for _, key := range keys {
		targets = append(targets, *inboxes[key])
	}

	return targets, nil
}

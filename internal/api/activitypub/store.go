package activitypub

import (
	"context"

	"github.com/deicod/gotodon/internal/db"
)

type Store interface {
	GetAccountByUsername(ctx context.Context, username string) (db.Account, error)
	GetAccountByURI(ctx context.Context, uri string) (db.Account, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
	GetConversationByParentAccountAndStatus(ctx context.Context, arg db.GetConversationByParentAccountAndStatusParams) (db.Conversation, error)
	GetConversationsByIDs(ctx context.Context, ids []int64) ([]db.Conversation, error)
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	GetStatusStats(ctx context.Context, statusID int64) (db.StatusStat, error)
	GetStatusStatsByIDs(ctx context.Context, statusIds []int64) ([]db.StatusStat, error)
	GetQuote(ctx context.Context, id int64) (db.Quote, error)
	GetAccountOutboxStatuses(ctx context.Context, arg db.GetAccountOutboxStatusesParams) ([]db.Status, error)
	CountAccountOutboxStatuses(ctx context.Context, accountID int64) (int64, error)
	GetStatusesByIDs(ctx context.Context, ids []int64) ([]db.Status, error)
	ListConversationStatuses(ctx context.Context, arg db.ListConversationStatusesParams) ([]db.Status, error)
	ListStatusReplies(ctx context.Context, arg db.ListStatusRepliesParams) ([]db.Status, error)
	ListFollowerURIsByPrefix(ctx context.Context, arg db.ListFollowerURIsByPrefixParams) ([]string, error)
	GetMentionsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.Mention, error)
	GetTagsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.GetTagsByStatusIDsRow, error)
	GetMediaAttachmentsByStatusIDs(ctx context.Context, statusIds []int64) ([]db.MediaAttachment, error)
	GetFollowers(ctx context.Context, arg db.GetFollowersParams) ([]db.Account, error)
	GetFollowing(ctx context.Context, arg db.GetFollowingParams) ([]db.Account, error)
	CountFollowers(ctx context.Context, accountID int64) (int64, error)
	CountFollowing(ctx context.Context, accountID int64) (int64, error)
	GetPinnedStatuses(ctx context.Context, accountID int64) ([]db.Status, error)
	ListFeaturedTags(ctx context.Context, accountID int64) ([]db.FeaturedTag, error)
}

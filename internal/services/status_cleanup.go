package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/streaming"
	"github.com/deicod/gotodon/pkg/snowflake"
)

const (
	statusCleanupMaxBudget         = 300
	statusCleanupPerPolicyBudget   = 5
	statusCleanupPolicyPageSize    = 100
	statusCleanupCutoffLimit       = 5000
	statusCleanupPolicyCursorTTL   = time.Hour
	statusCleanupAccountInspectTTL = 14 * 24 * time.Hour
)

var ErrStatusCleanupMissingDeps = errors.New("status cleanup missing dependencies")

// StatusCleanupStore describes DB access for status cleanup.
type StatusCleanupStore interface {
	ListAccountStatusesCleanupPolicies(ctx context.Context, arg db.ListAccountStatusesCleanupPoliciesParams) ([]db.AccountStatusesCleanupPolicy, error)
	GetStatusCleanupCutoffID(ctx context.Context, arg db.GetStatusCleanupCutoffIDParams) (int64, error)
	ListStatusesForCleanup(ctx context.Context, arg db.ListStatusesForCleanupParams) ([]db.Status, error)
	DeleteStatus(ctx context.Context, id int64) error
	DecrementStatusReplies(ctx context.Context, id int64) error
	GetMentionsByStatusID(ctx context.Context, statusID int64) ([]db.Mention, error)
	GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error)
}

// StatusCleanupService deletes statuses according to per-account cleanup policies.
type StatusCleanupService struct {
	store     StatusCleanupStore
	redis     *redis.Client
	outgoing  federation.OutgoingDeliverer
	publisher streaming.Publisher
	logger    *slog.Logger
	now       func() time.Time

	maxBudget       int
	perPolicyBudget int
	policyPageSize  int32
	cutoffLimit     int32
}

// NewStatusCleanupService constructs a status cleanup service with defaults.
func NewStatusCleanupService(store StatusCleanupStore, redisClient *redis.Client, outgoing federation.OutgoingDeliverer, publisher streaming.Publisher) *StatusCleanupService {
	return &StatusCleanupService{
		store:           store,
		redis:           redisClient,
		outgoing:        outgoing,
		publisher:       publisher,
		logger:          slog.Default(),
		now:             time.Now,
		maxBudget:       statusCleanupMaxBudget,
		perPolicyBudget: statusCleanupPerPolicyBudget,
		policyPageSize:  statusCleanupPolicyPageSize,
		cutoffLimit:     statusCleanupCutoffLimit,
	}
}

// Run processes cleanup policies up to the configured budget.
func (s *StatusCleanupService) Run(ctx context.Context) error {
	if s == nil || s.store == nil {
		return ErrStatusCleanupMissingDeps
	}
	budget := s.maxBudget
	if budget <= 0 {
		return nil
	}
	perPolicyBudget := s.perPolicyBudget
	if perPolicyBudget <= 0 {
		perPolicyBudget = statusCleanupPerPolicyBudget
	}
	pageSize := s.policyPageSize
	if pageSize <= 0 {
		pageSize = statusCleanupPolicyPageSize
	}

	afterID := s.lastPolicyID(ctx)
	triedReset := false

	for budget > 0 {
		policies, err := s.store.ListAccountStatusesCleanupPolicies(ctx, db.ListAccountStatusesCleanupPoliciesParams{
			AfterID: afterID,
			Limit:   pageSize,
		})
		if err != nil {
			return err
		}
		if len(policies) == 0 {
			if afterID == 0 || triedReset {
				s.clearLastPolicyID(ctx)
				return nil
			}
			afterID = 0
			triedReset = true
			continue
		}

		for _, policy := range policies {
			remaining := budget
			if remaining > perPolicyBudget {
				remaining = perPolicyBudget
			}
			deleted, err := s.cleanupPolicy(ctx, policy, remaining)
			if err != nil {
				return err
			}
			budget -= deleted
			afterID = policy.ID
			if budget <= 0 {
				break
			}
		}

		if budget <= 0 {
			if afterID > 0 {
				s.saveLastPolicyID(ctx, afterID)
			}
			return nil
		}

		if len(policies) < int(pageSize) {
			break
		}
	}

	s.clearLastPolicyID(ctx)
	return nil
}

func (s *StatusCleanupService) cleanupPolicy(ctx context.Context, policy db.AccountStatusesCleanupPolicy, limit int) (int, error) {
	if !policy.Enabled || limit <= 0 {
		return 0, nil
	}
	if policy.AccountID <= 0 {
		return 0, nil
	}
	if policy.MinStatusAge <= 0 {
		return 0, nil
	}

	lastInspected := s.lastInspected(ctx, policy.AccountID)
	maxID := snowflakeIDAt(s.now().Add(-time.Duration(policy.MinStatusAge) * time.Second))
	if maxID <= 0 || maxID <= lastInspected {
		return 0, nil
	}

	cutoffLimit := s.cutoffLimit
	if cutoffLimit <= 0 {
		cutoffLimit = statusCleanupCutoffLimit
	}
	cutoffID, err := s.store.GetStatusCleanupCutoffID(ctx, db.GetStatusCleanupCutoffIDParams{
		AccountID: policy.AccountID,
		MinID:     lastInspected,
		MaxID:     maxID,
		Limit:     cutoffLimit,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	if cutoffID <= 0 {
		return 0, nil
	}

	statuses, err := s.store.ListStatusesForCleanup(ctx, db.ListStatusesForCleanupParams{
		AccountID:        policy.AccountID,
		MaxID:            cutoffID,
		MinID:            lastInspected,
		KeepDirect:       policy.KeepDirect,
		KeepPinned:       policy.KeepPinned,
		KeepPolls:        policy.KeepPolls,
		KeepMedia:        policy.KeepMedia,
		KeepSelfFav:      policy.KeepSelfFav,
		KeepSelfBookmark: policy.KeepSelfBookmark,
		MinFavs:          policy.MinFavs,
		MinReblogs:       policy.MinReblogs,
		Limit:            int32(limit),
	})
	if err != nil {
		return 0, err
	}

	deleted := 0
	lastDeleted := int64(0)
	for _, status := range statuses {
		if err := s.deleteStatus(ctx, status); err != nil {
			return deleted, err
		}
		deleted++
		lastDeleted = status.ID
	}

	inspectID := cutoffID
	if lastDeleted > 0 {
		inspectID = lastDeleted
	}
	s.recordLastInspected(ctx, policy.AccountID, inspectID)

	return deleted, nil
}

func (s *StatusCleanupService) deleteStatus(ctx context.Context, status db.Status) error {
	if err := s.store.DeleteStatus(ctx, status.ID); err != nil {
		return err
	}
	if status.InReplyToID.Valid {
		if err := s.store.DecrementStatusReplies(ctx, status.InReplyToID.Int64); err != nil {
			s.logError("Failed to decrement reply count", "status_id", status.ID, "error", err)
		}
	}

	if s.outgoing != nil {
		mentions, err := s.loadMentionAccounts(ctx, status.ID)
		if err != nil {
			s.logError("Failed to load mentions for cleanup", "status_id", status.ID, "error", err)
		} else if err := s.outgoing.DeliverStatusDelete(ctx, status, mentions); err != nil {
			s.logError("Failed to deliver cleanup delete", "status_id", status.ID, "error", err)
		}
	}

	if s.publisher != nil {
		streams := []string{fmt.Sprintf("user:%d", status.AccountID)}
		if status.Visibility == 0 {
			streams = append(streams, streaming.StreamPublic, streaming.StreamPublicLocal)
		}
		if err := s.publisher.PublishDeleteToRedis(ctx, strconv.FormatInt(status.ID, 10), streams); err != nil {
			s.logError("Failed to publish cleanup delete", "status_id", status.ID, "error", err)
		}
	}

	return nil
}

func (s *StatusCleanupService) loadMentionAccounts(ctx context.Context, statusID int64) ([]db.Account, error) {
	mentions, err := s.store.GetMentionsByStatusID(ctx, statusID)
	if err != nil {
		return nil, err
	}
	if len(mentions) == 0 {
		return nil, nil
	}

	ids := make([]int64, 0, len(mentions))
	seen := make(map[int64]struct{})
	for _, mention := range mentions {
		if mention.AccountID == 0 {
			continue
		}
		if _, ok := seen[mention.AccountID]; ok {
			continue
		}
		seen[mention.AccountID] = struct{}{}
		ids = append(ids, mention.AccountID)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	return s.store.GetAccountsByIDs(ctx, ids)
}

func (s *StatusCleanupService) lastPolicyID(ctx context.Context) int64 {
	if s.redis == nil {
		return 0
	}
	value, err := s.redis.Get(ctx, statusCleanupPolicyCursorKey).Result()
	if err != nil {
		return 0
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func (s *StatusCleanupService) saveLastPolicyID(ctx context.Context, id int64) {
	if s.redis == nil || id <= 0 {
		return
	}
	if err := s.redis.Set(ctx, statusCleanupPolicyCursorKey, id, statusCleanupPolicyCursorTTL).Err(); err != nil {
		s.logError("Failed to save cleanup policy cursor", "error", err)
	}
}

func (s *StatusCleanupService) clearLastPolicyID(ctx context.Context) {
	if s.redis == nil {
		return
	}
	if err := s.redis.Del(ctx, statusCleanupPolicyCursorKey).Err(); err != nil {
		s.logError("Failed to clear cleanup policy cursor", "error", err)
	}
}

func (s *StatusCleanupService) lastInspected(ctx context.Context, accountID int64) int64 {
	if s.redis == nil || accountID <= 0 {
		return 0
	}
	value, err := s.redis.Get(ctx, statusCleanupAccountKey(accountID)).Result()
	if err != nil {
		return 0
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func (s *StatusCleanupService) recordLastInspected(ctx context.Context, accountID, statusID int64) {
	if s.redis == nil || accountID <= 0 || statusID <= 0 {
		return
	}
	if err := s.redis.Set(ctx, statusCleanupAccountKey(accountID), statusID, statusCleanupAccountInspectTTL).Err(); err != nil {
		s.logError("Failed to record cleanup inspection", "account_id", accountID, "error", err)
	}
}

func (s *StatusCleanupService) logError(msg string, args ...any) {
	if s.logger == nil {
		slog.Default().Error(msg, args...)
		return
	}
	s.logger.Error(msg, args...)
}

func statusCleanupAccountKey(accountID int64) string {
	return fmt.Sprintf("account_cleanup:%d", accountID)
}

func snowflakeIDAt(t time.Time) int64 {
	ms := t.UnixMilli()
	if ms <= snowflake.MastodonEpoch {
		return 0
	}
	return (ms - snowflake.MastodonEpoch) << snowflake.SequenceBits
}

const statusCleanupPolicyCursorKey = "account_statuses_cleanup_scheduler:last_policy_id"
